package hubtest

import (
	"os"
	"testing"

	"github.com/srjn45/wdx/internal/hub/server"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/waittest"
)

func TestMain(m *testing.M) { os.Exit(runtest.Main(m)) }

// A test hub's waits on the nodes are what is left of the -timeout, the one
// a test shortens excepted, unless the run keeps wdx's own.
func TestStartLimits(t *testing.T) {
	t.Parallel()
	before := waittest.Limit(t)
	got := Start(t, func(o *server.Options) { o.Limits.Bye = 1 }).opts.Limits
	after := waittest.Limit(t)
	if runtest.ProductionLimits() {
		if got != (server.Limits{Bye: 1}) {
			t.Fatalf("limits %+v, want the hub's own but Bye", got)
		}
		return
	}
	long := got.Hello
	if long > before || long < after || got != (server.Limits{Hello: long, Ping: long, Bye: 1, StreamJoin: long, StreamClose: long}) {
		t.Fatalf("limits %+v, want each what is left of the -timeout but Bye", got)
	}
}
