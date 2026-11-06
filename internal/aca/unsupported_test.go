package aca_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/aca/acatest"
	"github.com/srjn45/wdx/internal/screen"
)

func TestStubsSayNotSupported(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	bare := &acatest.Bare{Name: "bare"}
	if _, err := bare.BackUp(ctx, aca.Launch{}); !errors.Is(err, aca.ErrNotSupported) {
		t.Errorf("BackUp: %v", err)
	}
	if err := bare.BeginSession(ctx, aca.Launch{}, "s-2"); !errors.Is(err, aca.ErrNotSupported) {
		t.Errorf("BeginSession: %v", err)
	}
	if _, err := bare.Brief(ctx, aca.Launch{}, 100); !errors.Is(err, aca.ErrNotSupported) {
		t.Errorf("Brief: %v", err)
	}
	if got, err := bare.Detached(ctx, aca.Launch{}); err != nil || len(got) != 0 {
		t.Errorf("Detached = %v, %v; want nothing", got, err)
	}
	if err := bare.Steer(io.Discard, "hello"); !errors.Is(err, aca.ErrNotSupported) {
		t.Errorf("Steer: %v", err)
	}
}

// Codex, Claude Code and Antigravity steer; the others stop a turn for an instant
// message.
func TestCanSteer(t *testing.T) {
	t.Parallel()
	for _, a := range aca.All() {
		if got, want := aca.CanSteer(a), a.ID() == aca.CodexID || a.ID() == aca.ClaudeCodeID || a.ID() == aca.AntigravityID; got != want {
			t.Errorf("CanSteer(%s) = %v, want %v", a.ID(), got, want)
		}
	}
	if aca.CanSteer(&acatest.ACA{}) || aca.CanSteer(&acatest.Bare{Name: "bare"}) {
		t.Error("a test ACA steers")
	}
}

func TestCanBeginSession(t *testing.T) {
	t.Parallel()
	if !aca.CanBeginSession(aca.ClaudeCode{}) {
		t.Error("Claude Code cannot begin a session")
	}
	if !aca.CanBeginSession(&acatest.ACA{}) {
		t.Error("the test ACA cannot begin a session")
	}
	if aca.CanBeginSession(&acatest.Bare{Name: "bare"}) {
		t.Error("an ACA with NoBeginSession can begin a session")
	}
}

func TestNoScreenParserLeavesItToMotion(t *testing.T) {
	t.Parallel()
	got := aca.NoScreenParser{}.Parse([]string{"$ codex", "> "}, []string{"$ codex", "> "})
	want := screen.ScreenState{Activity: screen.ActivityUnknown, InputBox: true}
	if got != want {
		t.Errorf("Parse = %+v, want %+v", got, want)
	}
	m := screen.NewMotion(screen.MotionRules{Still: screen.StillTime, Echo: screen.EchoTime})
	m.Screen(0, true, got)
	if a := m.State().Activity; a != screen.ActivityWorking {
		t.Errorf("moving: %s, want working", a)
	}
	m.Advance(screen.StillTime)
	if a := m.State().Activity; a != screen.ActivityIdle {
		t.Errorf("still: %s, want idle", a)
	}
}

func TestAllIsTheKnownList(t *testing.T) {
	t.Parallel()
	ids := aca.IDs()
	if len(ids) == 0 || ids[0] != aca.Default().ID() {
		t.Fatalf("IDs = %v, Default = %s", ids, aca.Default().ID())
	}
	for _, a := range aca.All() {
		if got, ok := aca.ByID(a.ID()); !ok || got.ID() != a.ID() {
			t.Errorf("ByID(%s) = %v, %v", a.ID(), got, ok)
		}
		if a.Program() == "" {
			t.Errorf("%s has no program", a.ID())
		}
	}
}
