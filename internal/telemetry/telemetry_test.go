package telemetry

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/srjn45/wdx/internal/logging"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) count(msg string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Count(s.b.String(), `"msg":"`+msg+`"`)
}

func TestNothingSentWithoutEndpoint(t *testing.T) {
	t.Parallel()
	var logs syncBuffer
	provider, stop, err := Start(logging.NewContext(context.Background(), logging.New(&logs, slog.LevelInfo)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.(noop.MeterProvider); !ok {
		t.Fatalf("want a provider that measures nothing, got %T", provider)
	}
	if err := stop(context.Background()); err != nil || logs.count("metrics not sent") != 1 {
		t.Fatalf("want it logged that nothing is sent, got %v: %s", err, logs.b.String())
	}
}

// TestSendFailuresLogged checks a collector that refuses the metrics is
// logged once, not at every send, and that it is logged when sending works
// again.
func TestSendFailuresLogged(t *testing.T) {
	t.Parallel()
	var refusing atomic.Bool
	var received atomic.Int32
	refusing.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refusing.Load() {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received.Add(1)
	}))
	defer srv.Close()

	var logs syncBuffer
	ctx := logging.NewContext(context.Background(), logging.New(&logs, slog.LevelInfo))
	provider, stop, err := Start(ctx, Options{Endpoint: srv.URL + "/", Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	runs, err := provider.Meter("test").Int64Counter("runs")
	if err != nil {
		t.Fatal(err)
	}
	runs.Add(ctx, 1)
	send := provider.(*sdkmetric.MeterProvider).ForceFlush
	for range 3 {
		if err := send(ctx); err != nil {
			t.Fatalf("want a failed send dropped, got %v", err)
		}
	}
	if n := logs.count("metrics could not be sent"); n != 1 {
		t.Fatalf("want the failure logged once, got %d: %s", n, logs.b.String())
	}

	refusing.Store(false)
	if err := stop(ctx); err != nil {
		t.Fatal(err)
	}
	if received.Load() != 1 || logs.count("metrics sent again") != 1 {
		t.Fatalf("want the last metrics sent on stop and logged, got %d sends: %s", received.Load(), logs.b.String())
	}
}
