package limitstest

import (
	"os"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/runtest"
)

func TestMain(m *testing.M) { os.Exit(runtest.Main(m)) }

func TestLengthen(t *testing.T) {
	t.Parallel()
	limits := struct {
		Dial, Write time.Duration
		Tries       int
	}{Write: time.Second, Tries: 3}
	Lengthen(t, &limits)
	if runtest.ProductionLimits() {
		if limits.Dial != 0 || limits.Write != time.Second {
			t.Fatalf("limits changed under production limits: %+v", limits)
		}
		return
	}
	if limits.Dial <= 0 || limits.Write != limits.Dial || limits.Tries != 3 {
		t.Fatalf("limits = %+v, want every duration lengthened and nothing else", limits)
	}
}
