package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/aca/acatest"
	"github.com/srjn45/wdx/internal/terminal"
)

// machine stands in for what is installed, and counts the looks.
type machine struct {
	mu        sync.Mutex
	installed map[string]bool
	looks     int
}

func (m *machine) look(_ context.Context, program string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.looks++
	if !m.installed[program] {
		return fmt.Errorf("%s: not found", program)
	}
	return nil
}

func (m *machine) set(program string, installed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.installed[program] = installed
}

func (m *machine) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.looks
}

// twoACAs are two ACAs on a machine with both installed, kept for age.
func twoACAs(age time.Duration) (*ACAs, *machine) {
	m := &machine{installed: map[string]bool{"one": true, "two": true}}
	known := []aca.AiCodingAgent{&acatest.Bare{Name: "first", ProgramName: "one"}, &acatest.Bare{Name: "second", ProgramName: "two"}}
	return NewACAs(known, m.look, age), m
}

// always is a check age that looks again at every request.
const always = time.Nanosecond

func reasonOf(err error) (string, bool) {
	e, ok := errors.AsType[*UnavailableError](err)
	if !ok {
		return "", false
	}
	return e.Reason, true
}

// Within the check age a list goes by what was found.
func TestAListGoesByTheLastCheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	acas, m := twoACAs(0)
	if got := ids(acas.Usable(ctx)); fmt.Sprint(got) != "[first second]" {
		t.Fatalf("usable = %v", got)
	}
	m.set("two", false)
	if got := ids(acas.Usable(ctx)); fmt.Sprint(got) != "[first second]" {
		t.Fatalf("usable = %v", got)
	}
	if m.count() != 2 {
		t.Errorf("looks = %d, want one check of two", m.count())
	}
	acas.SetEnabled(map[string]bool{"first": false})
	if got := ids(acas.Usable(ctx)); fmt.Sprint(got) != "[second]" {
		t.Fatalf("usable with first disabled = %v", got)
	}
}

func TestWhyAnACACannotBeUsed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	acas, m := twoACAs(always)
	acas.SetEnabled(map[string]bool{"first": false})
	m.set("two", false)
	if reason, ok := reasonOf(second(acas.ForNew(ctx, "first"))); !ok || reason != ReasonDisabled {
		t.Errorf("a disabled one: %q, %v", reason, ok)
	}
	if reason, ok := reasonOf(second(acas.ForNew(ctx, "second"))); !ok || reason != ReasonNotInstalled {
		t.Errorf("one not installed: %q, %v", reason, ok)
	}
	if reason, ok := reasonOf(second(acas.ForNew(ctx, ""))); !ok || reason != "" {
		t.Errorf("none usable: %q, %v", reason, ok)
	}
	if err := second(acas.ForNew(ctx, "third")); !errors.Is(err, ErrUnknownACA) {
		t.Errorf("an unknown one: %v", err)
	}
	if got := acas.Usable(ctx); len(got) != 0 {
		t.Errorf("usable = %v", ids(got))
	}
}

// A refusal looks again first, so one installed seconds ago is never refused,
// however young the last check.
func TestARefusalLooksAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	acas, m := twoACAs(0)
	m.set("one", false)
	if got := ids(acas.Usable(ctx)); fmt.Sprint(got) != "[second]" {
		t.Fatalf("usable = %v", got)
	}
	m.set("one", true)
	chosen, err := acas.ForNew(ctx, "first")
	if err != nil || chosen.ID() != "first" {
		t.Fatalf("ForNew(first) = %v, %v", chosen, err)
	}
	if chosen, err := acas.ForNew(ctx, ""); err != nil || chosen.ID() != "first" {
		t.Fatalf("the default = %v, %v", chosen, err)
	}
}

func ids(list []aca.AiCodingAgent) []string {
	var out []string
	for _, a := range list {
		out = append(out, a.ID())
	}
	return out
}

// Disabling is for new agents: one that exists still starts. One whose program is
// gone is refused before anything stops.
func TestStartsGoByInstalledOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "exit 0")
	a := f.create(t, "helper")
	f.waitState(t, a.ID, terminal.StateExited)

	f.agents.ACAs().SetEnabled(map[string]bool{f.aca.ID(): false})
	if _, err := f.agents.Create(ctx, f.project, "another", f.dir, "", Launch{ACA: f.aca.ID()}); err == nil {
		t.Fatal("a new agent ran a disabled ACA")
	}
	if _, _, err := f.agents.Start(ctx, a.ID); err != nil {
		t.Fatalf("Start of a disabled ACA's agent: %v", err)
	}
	f.waitState(t, a.ID, terminal.StateExited)

	f.aca.ProgramName = acatest.NotInstalled
	if _, _, err := f.agents.Start(ctx, a.ID); !isUnavailable(err) {
		t.Fatalf("Start of an uninstalled ACA's agent: %v", err)
	}
	if _, _, err := f.agents.Restart(ctx, a.ID, true); !isUnavailable(err) {
		t.Fatalf("Restart of an uninstalled ACA's agent: %v", err)
	}
	got, err := f.agents.Store().Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != a.SessionID || got.Terminal.State != terminal.StateExited {
		t.Errorf("after the refusals: %+v", got)
	}
}

func isUnavailable(err error) bool {
	reason, ok := reasonOf(err)
	return ok && reason == ReasonNotInstalled
}
