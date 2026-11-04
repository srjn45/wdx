package agent

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// DefaultCheckAge is how long ACAs keeps what it found installed before it looks
// again for a list. A refusal never goes by it: it looks again first.
const DefaultCheckAge = 30 * time.Second

// Why an AI coding agent cannot be used, as UnavailableError.Reason says.
const (
	ReasonNotInstalled = "not_installed"
	ReasonDisabled     = "disabled"
)

// UnavailableError is an AI coding agent that cannot be used: ACA and Reason
// say which and why, both "" when none was asked for and none can be.
type UnavailableError struct {
	ACA, Program, Reason string
}

func (e *UnavailableError) Error() string {
	switch e.Reason {
	case ReasonNotInstalled:
		return fmt.Sprintf("the AI coding agent %s is not installed: %s is not found through the login shell", e.ACA, e.Program)
	case ReasonDisabled:
		return fmt.Sprintf("the AI coding agent %s is disabled for new agents", e.ACA)
	}
	return "no AI coding agent is installed and enabled"
}

// ACAs are the AI coding agents a daemon knows, and which of them are usable:
// installed, as its program is found through the login shell, and enabled. It
// looks only when asked, never on a timer, every one at once, and keeps what it
// found in memory for the check age. docs/ACA.md.
type ACAs struct {
	known []aca.AiCodingAgent
	look  func(ctx context.Context, program string) error
	age   time.Duration

	checking sync.Mutex // one look at a time; those asking meanwhile wait for it

	mu        sync.Mutex
	enabled   map[string]bool // by id; one missing is enabled
	installed map[string]bool // by id, as last found
	checked   time.Time       // zero until the first look
}

// NewACAs returns the ACAs of known, which look finds the program of, kept for age,
// DefaultCheckAge when 0. Every one is enabled until SetEnabled says otherwise.
func NewACAs(known []aca.AiCodingAgent, look func(ctx context.Context, program string) error, age time.Duration) *ACAs {
	if age == 0 {
		age = DefaultCheckAge
	}
	return &ACAs{known: known, look: look, age: age}
}

// SetEnabled replaces which ACAs are enabled, by id.
func (s *ACAs) SetEnabled(enabled map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = maps.Clone(enabled)
}

// Known is the ACA with id, or nil when the daemon does not know it.
func (s *ACAs) Known(id string) aca.AiCodingAgent {
	if s == nil {
		return nil
	}
	for _, a := range s.known {
		if a.ID() == id {
			return a
		}
	}
	return nil
}

// Usable are the ACAs installed and enabled, in their order; the first is what new
// agents run unless asked.
func (s *ACAs) Usable(ctx context.Context) []aca.AiCodingAgent {
	s.check(ctx, false)
	return s.usable()
}

func (s *ACAs) usable() []aca.AiCodingAgent {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []aca.AiCodingAgent
	for _, a := range s.known {
		if s.installed[a.ID()] && s.isEnabled(a.ID()) {
			list = append(list, a)
		}
	}
	return list
}

// isEnabled says whether id is enabled; an s.mu holder only.
func (s *ACAs) isEnabled(id string) bool {
	enabled, set := s.enabled[id]
	return enabled || !set
}

// ForNew is the ACA a new agent runs: id, or the first usable when "". An id the
// daemon does not know is ErrUnknownACA, and one it cannot use an *UnavailableError,
// after looking again whether it was installed meanwhile.
func (s *ACAs) ForNew(ctx context.Context, id string) (aca.AiCodingAgent, error) {
	if id == "" {
		if list := s.Usable(ctx); len(list) > 0 {
			return list[0], nil
		}
		if s.check(ctx, true); len(s.usable()) == 0 {
			return nil, &UnavailableError{}
		}
		return s.usable()[0], nil
	}
	a := s.Known(id)
	if a == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownACA, id)
	}
	s.mu.Lock()
	enabled := s.isEnabled(id)
	s.mu.Unlock()
	if !enabled {
		logging.FromContext(ctx).Info("aca refused", "aca", id, "reason", ReasonDisabled)
		return nil, &UnavailableError{ACA: id, Reason: ReasonDisabled}
	}
	return a, s.Installed(ctx, a)
}

// Installed fails with an *UnavailableError when a is not installed, after looking
// again: one installed seconds ago is never refused. A start goes by this only, as
// disabling an ACA is for new agents.
func (s *ACAs) Installed(ctx context.Context, a aca.AiCodingAgent) error {
	s.check(ctx, false)
	if s.isInstalled(a.ID()) {
		return nil
	}
	if s.check(ctx, true); s.isInstalled(a.ID()) {
		return nil
	}
	logging.FromContext(ctx).Info("aca refused", "aca", a.ID(), "reason", ReasonNotInstalled, "program", a.Program())
	return &UnavailableError{ACA: a.ID(), Program: a.Program(), Reason: ReasonNotInstalled}
}

func (s *ACAs) isInstalled(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.installed[id]
}

// check looks for every ACA's program at once, unless what it found is younger than
// the check age and fresh is false. A look that began while it waited is enough.
func (s *ACAs) check(ctx context.Context, fresh bool) {
	asked := time.Now()
	s.checking.Lock()
	defer s.checking.Unlock()
	s.mu.Lock()
	checked := s.checked
	s.mu.Unlock()
	if !checked.IsZero() && (checked.After(asked) || !fresh && time.Since(checked) < s.age) {
		return
	}
	began := time.Now()
	found := make([]bool, len(s.known))
	var wg sync.WaitGroup
	for i, a := range s.known {
		wg.Go(func() {
			err := s.look(ctx, a.Program())
			found[i] = err == nil
			logging.FromContext(ctx).Info("aca looked for", "aca", a.ID(), "program", a.Program(), "installed", found[i], "err", err)
		})
	}
	wg.Wait()
	installed := map[string]bool{}
	for i, a := range s.known {
		installed[a.ID()] = found[i]
	}
	s.mu.Lock()
	s.installed, s.checked = installed, began
	s.mu.Unlock()
	logging.FromContext(ctx).Info("acas checked", "count", len(s.known), "duration_ms", time.Since(began).Milliseconds(), "fresh", fresh)
}

// notKnown is the error for an agent whose ACA this daemon does not know.
func notKnown(id string) error {
	return fmt.Errorf("this %s does not know the AI coding agent %q", identity.Name, id)
}
