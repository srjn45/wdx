package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/terminal"
)

// An ACA that has written only stubs runs, starts and is removed, but has no
// transcript, brief or restart to give, and says so.
func TestAnACAWithStubsSaysWhatItCannotDo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newBareFixture(t)
	a := f.create(t, "helper")
	if a.ACA != "bare" || a.Terminal.State != terminal.StateRunning {
		t.Fatalf("agent = %+v", a)
	}
	if _, err := f.agents.Transcript(ctx, a.ID, nil, 10); !errors.Is(err, aca.ErrNotSupported) {
		t.Errorf("Transcript: %v", err)
	}
	if _, err := f.agents.Brief(ctx, a.ID); !errors.Is(err, aca.ErrNotSupported) {
		t.Errorf("Brief: %v", err)
	}
	if _, _, err := f.agents.Restart(ctx, a.ID, true); !errors.Is(err, aca.ErrNotSupported) {
		t.Fatalf("Restart: %v", err)
	}
	// Refused before anything stopped or moved.
	got, err := f.agents.Store().Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != a.SessionID || got.Terminal.State != terminal.StateRunning || got.Terminal.PID != a.Terminal.PID {
		t.Errorf("after the refused restart: %+v", got)
	}
	if _, _, err := f.agents.Remove(ctx, a.ID, true); err != nil {
		t.Errorf("Remove: %v", err)
	}
}
