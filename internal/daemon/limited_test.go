package daemon

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/aca/acatest"
	"github.com/srjn45/wdx/internal/agent"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/telemetry"
	"github.com/srjn45/wdx/internal/terminal"
	"github.com/srjn45/wdx/internal/waittest"
)

// limitText is l as a test reads it: the bucket and when it resets, or "none".
func limitText(l *api.AgentLimited, now time.Time) string {
	switch {
	case l == nil:
		return "none"
	case l.ResetsAt == nil:
		return "[" + l.Bucket + "] no reset"
	}
	return "[" + l.Bucket + "] " + l.ResetsAt.Sub(now).String()
}

// TestAgentLimits: an agent is limited while a bucket its model draws on is, the
// one every model draws on or one its model names, and not while another is; a
// model its ACA does not know only while every bucket is; never when its ACA told
// nothing, once a reset passed or while it is not running.
func TestAgentLimits(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	test := &acatest.ACA{Buckets: map[string][]string{"argon": {"argon"}, "plain": nil, "Shown Argon": {"argon"}}}
	acas := agent.NewACAs([]aca.AiCodingAgent{test}, nil, 0)
	id := test.ID()
	row := func(bucket, window string, limited bool, resets time.Duration) agent.Usage {
		u := agent.Usage{ACA: id, Usage: aca.Usage{Window: window, Bucket: bucket, ObservedAt: now}, Limited: limited}
		if resets != 0 {
			u.ResetsAt = now.Add(resets)
		}
		return u
	}
	running := func(requested, shown string) agent.Agent {
		return agent.Agent{ACA: id, RequestedModel: requested, Model: shown, Terminal: terminal.Terminal{State: terminal.StateRunning}}
	}
	for _, tc := range []struct {
		name  string
		rows  []agent.Usage
		agent agent.Agent
		want  string
	}{
		{"its model's bucket", []agent.Usage{row("", "week", false, time.Hour), row("argon", "week", true, 2*time.Hour)}, running("argon", ""), "[argon] 2h0m0s"},
		{"its model's bucket by the name shown", []agent.Usage{row("argon", "week", true, 2*time.Hour)}, running("", "Shown Argon"), "[argon] 2h0m0s"},
		{"another model's bucket", []agent.Usage{row("", "week", false, time.Hour), row("argon", "week", true, 2*time.Hour)}, running("plain", ""), "none"},
		{"every model's bucket", []agent.Usage{row("", "week", true, time.Hour), row("argon", "week", false, 2*time.Hour)}, running("plain", ""), "[] 1h0m0s"},
		{"the latest reset of its buckets", []agent.Usage{row("", "week", true, 3*time.Hour), row("argon", "week", true, 2*time.Hour)}, running("argon", ""), "[] 3h0m0s"},
		{"a bucket with no reset is the last", []agent.Usage{row("", "week", true, 3*time.Hour), row("argon", "week", true, 0)}, running("argon", ""), "[argon] no reset"},
		{"no model, another bucket limited", []agent.Usage{row("", "week", false, time.Hour), row("argon", "week", true, 2*time.Hour)}, running("", ""), "none"},
		{"no model, every bucket limited", []agent.Usage{row("", "week", true, time.Hour), row("argon", "week", true, 2*time.Hour)}, running("", ""), "[argon] 2h0m0s"},
		{"a model not known, every bucket limited", []agent.Usage{row("argon", "week", true, 2*time.Hour)}, running("other", "Other"), "[argon] 2h0m0s"},
		{"a reset that passed", []agent.Usage{row("", "week", true, -time.Minute)}, running("plain", ""), "none"},
		{"a window limited till later", []agent.Usage{row("", "session", true, -time.Minute), row("", "week", true, time.Hour)}, running("plain", ""), "[] 1h0m0s"},
		{"its ACA told nothing", nil, running("argon", ""), "none"},
		{"another ACA's limit", []agent.Usage{{ACA: "codex", Usage: aca.Usage{Window: "week"}, Limited: true}}, running("", ""), "none"},
		{"not running", []agent.Usage{row("", "week", true, time.Hour)}, agent.Agent{ACA: id, Terminal: terminal.Terminal{State: terminal.StateExited}}, "none"},
	} {
		l := agentLimits{acas: acas, buckets: map[string][]api.UsageBucket{}}
		for _, b := range usageBuckets(tc.rows, now) {
			l.buckets[b.Aca] = append(l.buckets[b.Aca], b)
		}
		if got := limitText(l.of(tc.agent), now); got != tc.want {
			t.Errorf("%s: limited %s, want %s", tc.name, got, tc.want)
		}
	}
}

// limitOf is the limit of the agent named name in e, as limitText reads it.
func limitOf(e event, name string, now time.Time) string {
	if a, ok := agentNamed(e, name); ok {
		return limitText(a.Limited, now)
	}
	return "no agent"
}

// agentNamed is the agent named name in e.
func agentNamed(e event, name string) (api.Agent, bool) {
	for _, a := range agentsIn(e) {
		if a.Name == name {
			return a, true
		}
	}
	return api.Agent{}, false
}

// TestAgentLimitedServed: an agent's limit is worked out from the usage its ACA
// tells, listed, got and sent on the project stream as the usage changes.
func TestAgentLimitedServed(t *testing.T) {
	t.Parallel()
	td := startOn(t, shortDir(t), telemetry.Options{}, func(o *Options) {
		o.ACAs = []aca.AiCodingAgent{&acatest.ACA{Buckets: map[string][]string{"argon": {"argon"}, "plain": nil}}}
		o.StatusInterval = time.Millisecond
	})
	ctx := context.Background()
	app := td.create(t, "app").Worktrees[0].Path
	for name, model := range map[string]string{"on-argon": "argon", "on-plain": "plain", "default": ""} {
		r, err := td.client.CreateAgentWithResponse(ctx, api.CreateAgent{Dir: app, Name: optional(name), Model: optional(model)})
		if err != nil || r.JSON201 == nil {
			t.Fatalf("create %s: %v %s", name, err, r.Body)
		}
	}
	list, err := td.client.ListAgentsWithResponse(ctx, &api.ListAgentsParams{Project: optional("app")})
	if err != nil || list.JSON200 == nil || len(list.JSON200.Agents) != 3 {
		t.Fatalf("list: %v %s", err, list.Body)
	}
	backup := identity.AgentDir(td.home, list.JSON200.Agents[0].Id)
	resets := time.Now().Add(2 * time.Hour).Truncate(time.Millisecond)
	tell := func(everyModelLimited bool) {
		t.Helper()
		rows := []aca.Usage{
			{Window: "week", UsedPercent: 50, Limited: everyModelLimited, ResetsAt: resets.Add(time.Hour), ObservedAt: time.Now()},
			{Window: "week", Bucket: "argon", UsedPercent: 100, ResetsAt: resets, ObservedAt: time.Now()},
		}
		if err := acatest.WriteUsage(backup, rows); err != nil {
			t.Fatal(err)
		}
	}
	text := func(e event) string {
		return limitOf(e, "on-argon", resets) + ", " + limitOf(e, "on-plain", resets) + ", " + limitOf(e, "default", resets)
	}
	tell(false)
	td.waitProject(t, "app", "the agent on argon limited", func(e event) bool { return text(e) == "[argon] 0s, none, none" })

	list, err = td.client.ListAgentsWithResponse(ctx, &api.ListAgentsParams{Project: optional("app")})
	if err != nil || list.JSON200 == nil {
		t.Fatalf("list: %v %s", err, list.Body)
	}
	for _, a := range list.JSON200.Agents {
		want := "none"
		if a.Name == "on-argon" {
			want = "[argon] 0s"
		}
		if got := limitText(a.Limited, resets); got != want {
			t.Errorf("listed %s: limited %s, want %s", a.Name, got, want)
		}
	}
	got, err := td.client.GetAgentWithResponse(ctx, "on-argon", &api.GetAgentParams{Project: optional("app")})
	if err != nil || got.JSON200 == nil {
		t.Fatalf("get: %v %s", err, got.Body)
	}
	if l := limitText(got.JSON200.Agent.Limited, resets); l != "[argon] 0s" {
		t.Errorf("got: limited %s", l)
	}

	tell(true)
	td.waitProject(t, "app", "every agent limited", func(e event) bool { return text(e) == "[] 1h0m0s, [] 1h0m0s, [] 1h0m0s" })
}

// fakeClock is a time that moves only when a test moves it on, so that a test says
// when a reset passes.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	waits []fakeWait
	// added is closed, and made anew, each time a wait is added.
	added chan struct{}
}

type fakeWait struct {
	at time.Time
	ch chan time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.waits = append(c.waits, fakeWait{at: c.now.Add(d), ch: ch})
	close(c.added)
	c.added = make(chan struct{})
	return ch
}

// waitFor waits until a wait ending at at is pending.
func (c *fakeClock) waitFor(t *testing.T, at time.Time) {
	t.Helper()
	timeout := waittest.After(t)
	for {
		c.mu.Lock()
		added := c.added
		var pending []time.Time
		for _, w := range c.waits {
			pending = append(pending, w.at)
		}
		c.mu.Unlock()
		if slices.ContainsFunc(pending, at.Equal) {
			return
		}
		select {
		case <-added:
		case <-timeout:
			t.Fatalf("no wait until %v; pending %v", at, pending)
		}
	}
}

// advance moves the time on by d, ending the waits it passes.
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waits[:0]
	for _, w := range c.waits {
		if w.at.After(c.now) {
			kept = append(kept, w)
		} else {
			w.ch <- c.now
		}
	}
	c.waits = kept
}

// TestAgentLimitEndsAtItsReset: once the reset of its bucket passes, an agent is no
// longer limited, and the project stream says so with no usage told.
func TestAgentLimitEndsAtItsReset(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{now: time.Now(), added: make(chan struct{})}
	td := startOn(t, shortDir(t), telemetry.Options{}, func(o *Options) {
		o.ACAs = []aca.AiCodingAgent{&acatest.ACA{}}
		o.StatusInterval = time.Millisecond
		o.Hub.Clock = clock
	})
	ctx := context.Background()
	app := td.create(t, "app").Worktrees[0].Path
	a := td.createAgent(t, "", app, "helper")
	resets := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	if err := acatest.WriteUsage(identity.AgentDir(td.home, a.Id), []aca.Usage{{Window: "five_hour", UsedPercent: 100, ResetsAt: resets, ObservedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	events, leave := td.follow(t, "app")
	defer leave()
	waitEvent(t, events, "the agent limited", func(e event) bool { return e.name == "project" && limitOf(e, "helper", resets) == "[] 0s" })
	clock.waitFor(t, resets)
	clock.advance(resets.Sub(clock.Now()))
	waitEvent(t, events, "the limit ended", func(e event) bool { return e.name == "project" && limitOf(e, "helper", resets) == "none" })
	got, err := td.client.GetAgentWithResponse(ctx, "id:"+a.Id, nil)
	if err != nil || got.JSON200 == nil || got.JSON200.Agent.Limited != nil {
		t.Fatalf("got: %v %s", err, got.Body)
	}
}

// TestLimitResets: the streams are told at the earliest reset, read again at start
// and when woken, and not before it.
func TestLimitResets(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{now: time.Now(), added: make(chan struct{})}
	first, second := clock.Now().Add(time.Hour), clock.Now().Add(2*time.Hour)
	var mu sync.Mutex
	next := first
	notified := make(chan struct{}, 1)
	r := startLimitResets(context.Background(), func(context.Context) (time.Time, error) {
		mu.Lock()
		defer mu.Unlock()
		if !next.After(clock.Now()) {
			return time.Time{}, nil // the limit ended
		}
		return next, nil
	}, clock, func() { notified <- struct{}{} })
	defer r.close()
	clock.waitFor(t, first)
	mu.Lock()
	next = second
	mu.Unlock()
	r.woken()
	clock.waitFor(t, second)
	clock.advance(time.Hour) // the first reset's wait, which the second replaced
	clock.advance(time.Hour)
	select {
	case <-notified:
	case <-waittest.After(t):
		t.Fatal("not told at the reset")
	}
	select {
	case <-notified:
		t.Fatal("told twice")
	default:
	}
}
