package daemon

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/srjn45/wdx/internal/agent"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/hublink"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/terminal"
)

// agentLimits says which agents are limited by their accounts' usage, as of when it
// was read (the wdx doc AGENT_LIMITED). It is worked out each time agents are read,
// never stored, so that a limit ends by itself once its reset passes.
type agentLimits struct {
	acas *agent.ACAs
	// buckets are the usage buckets by ACA, each available or not as of now.
	buckets map[string][]api.UsageBucket
}

// limits reads the usage the agents' limits are worked out from, as of the
// daemon's clock.
func (s *server) limits(ctx context.Context) (agentLimits, error) {
	return readLimits(ctx, s.agents, s.clock.Now())
}

// readLimits reads the usage the limits of the agents of m are worked out from, as
// of now.
func readLimits(ctx context.Context, m *agent.Manager, now time.Time) (agentLimits, error) {
	rows, err := m.Store().Usage(ctx)
	if err != nil {
		return agentLimits{}, err
	}
	l := agentLimits{acas: m.ACAs(), buckets: map[string][]api.UsageBucket{}}
	for _, b := range usageBuckets(rows, now) {
		l.buckets[b.Aca] = append(l.buckets[b.Aca], b)
	}
	return l, nil
}

// usageBuckets groups rows into buckets, a window whose reset has passed by now no
// longer limited: the store keeps it until its ACA tells again.
func usageBuckets(rows []agent.Usage, now time.Time) []api.UsageBucket {
	windows := make([]api.UsageWindow, 0, len(rows))
	for _, u := range rows {
		windows = append(windows, api.UsageWindow{
			Aca: u.ACA, Window: u.Window, Bucket: u.Bucket, UsedPercent: u.UsedPercent, ResetsAt: optionalTime(u.ResetsAt),
			Limited: u.Limited && (u.ResetsAt.IsZero() || u.ResetsAt.After(now)), ObservedAt: u.ObservedAt,
		})
	}
	return api.UsageBuckets(windows)
}

// of is a's limit, nil when it is not limited: a bucket it draws on is not
// available, the one every model of its ACA draws on or one its model's ModelBuckets
// names. A model its ACA does not know is limited only while every bucket is, so that
// it never shows a limit that is not its own. An agent not running is never limited.
func (l agentLimits) of(a agent.Agent) *api.AgentLimited {
	buckets := l.buckets[a.ACA]
	if len(buckets) == 0 || a.Tombstone() || a.Terminal.State != terminal.StateRunning {
		return nil
	}
	var named []string
	known := false
	if impl := l.acas.Known(a.ACA); impl != nil {
		named, known = impl.ModelBuckets(a.RequestedModel, a.Model)
	}
	if !known {
		if slices.ContainsFunc(buckets, func(b api.UsageBucket) bool { return b.Available }) {
			return nil
		}
		return latestLimit(buckets)
	}
	return latestLimit(slices.DeleteFunc(slices.Clone(buckets), func(b api.UsageBucket) bool {
		return b.Bucket != "" && !slices.Contains(named, b.Bucket)
	}))
}

// latestLimit is the limited bucket among buckets that resets last, one that tells
// no reset counting as the last; nil when none is limited.
func latestLimit(buckets []api.UsageBucket) *api.AgentLimited {
	var limit *api.AgentLimited
	for _, b := range buckets {
		if b.Available {
			continue
		}
		resets := optionalTime(b.AvailableAt)
		if limit == nil || (limit.ResetsAt != nil && (resets == nil || resets.After(*limit.ResetsAt))) {
			limit = &api.AgentLimited{Bucket: b.Bucket, ResetsAt: resets}
		}
	}
	return limit
}

// nextReset is the earliest reset among the limited buckets, zero when none tells
// one: when an agent's limit may end with no usage told.
func (l agentLimits) nextReset() time.Time {
	var next time.Time
	for _, list := range l.buckets {
		for _, b := range list {
			if !b.Available && !b.AvailableAt.IsZero() && (next.IsZero() || b.AvailableAt.Before(next)) {
				next = b.AvailableAt
			}
		}
	}
	return next
}

// limitResets tells the project streams of the agents' change at each reset of a
// limited bucket, so that a limit ends on them with no usage told. Its timer is set
// at start and again each time it is woken, as when usage is stored.
type limitResets struct {
	// next reads the earliest reset among the limited buckets, zero for none.
	next   func(ctx context.Context) (time.Time, error)
	clock  hublink.Clock
	notify func()
	wake   chan struct{}
	stop   context.CancelFunc
	done   sync.WaitGroup
}

// startLimitResets starts the daemon's limitResets.
func (d *Daemon) startLimitResets() {
	next := func(ctx context.Context) (time.Time, error) {
		l, err := readLimits(ctx, d.agents, d.clock.Now())
		return l.nextReset(), err
	}
	d.limitResets = startLimitResets(logging.NewContext(context.Background(), d.log), next, d.clock, d.notices.notify)
}

// startLimitResets calls notify at each time next gives, read again at start and
// each time it is woken, until it is closed.
func startLimitResets(ctx context.Context, next func(context.Context) (time.Time, error), clock hublink.Clock, notify func()) *limitResets {
	ctx, cancel := context.WithCancel(ctx)
	r := &limitResets{next: next, clock: clock, notify: notify, wake: make(chan struct{}, 1), stop: cancel}
	r.done.Go(func() { r.follow(ctx) })
	return r
}

// woken asks the timer to be set again, from the usage as it is now. It never blocks.
func (r *limitResets) woken() {
	if r == nil {
		return // not started yet: it reads the usage when it starts
	}
	select {
	case r.wake <- struct{}{}:
	default: // one is already pending
	}
}

func (r *limitResets) close() {
	r.stop()
	r.done.Wait()
}

func (r *limitResets) follow(ctx context.Context) {
	log := logging.FromContext(ctx)
	var last time.Time
	for {
		next, err := r.next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error("usage not read for the agents' limits", "err", err)
		}
		if !next.Equal(last) && !next.IsZero() {
			log.Info("agents' limits told again at the next reset", "resets_at", next)
		}
		last = next
		var timer <-chan time.Time
		// One not ahead would fire at once, again and again: a limit past its reset
		// has ended.
		if now := r.clock.Now(); next.After(now) {
			timer = r.clock.After(next.Sub(now))
		}
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-timer:
			log.Info("a limited usage bucket reset", "resets_at", next)
			r.notify()
		}
	}
}
