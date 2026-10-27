package daemon

import (
	"context"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	colmetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"

	"github.com/srjn45/warden/internal/telemetry"
)

// collector receives metrics over OTLP/HTTP as an OpenTelemetry collector
// does, keeping the last send.
type collector struct {
	mu   sync.Mutex
	last *colmetricpb.ExportMetricsServiceRequest
}

func (c *collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	req := &colmetricpb.ExportMetricsServiceRequest{}
	if r.URL.Path != "/v1/metrics" || proto.Unmarshal(b, req) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	c.last = req
	c.mu.Unlock()
}

// requestCounts returns the count of wdx.request.duration in the last send by
// its labels, written as "code=not_found operation=getProject status=404".
func (c *collector) requestCounts(t *testing.T) map[string]uint64 {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		t.Fatal("no metrics sent")
	}
	counts := map[string]uint64{}
	for _, rm := range c.last.ResourceMetrics {
		if service := labels(rm.Resource.Attributes); !strings.Contains(service, "service.name=wdx ") {
			t.Errorf("want the metrics sent as service wdx, got %s", service)
		}
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name != "wdx.request.duration" {
					continue
				}
				if m.Unit != "s" {
					t.Errorf("want the duration in seconds, got %q", m.Unit)
				}
				for _, p := range m.GetHistogram().DataPoints {
					counts[labels(p.Attributes)] = p.Count
				}
			}
		}
	}
	return counts
}

func labels(kvs []*commonpb.KeyValue) string {
	var parts []string
	for _, kv := range kvs {
		v := kv.Value.GetStringValue()
		if _, ok := kv.Value.Value.(*commonpb.AnyValue_IntValue); ok {
			v = strconv.FormatInt(kv.Value.GetIntValue(), 10)
		}
		parts = append(parts, kv.Key+"="+v)
	}
	slices.Sort(parts)
	return strings.Join(parts, " ")
}

func TestRequestsMeasured(t *testing.T) {
	t.Parallel()
	c := &collector{}
	srv := httptest.NewServer(c)
	defer srv.Close()
	// The interval never passes: the daemon sends what it measured when it
	// stops.
	td := startMeasured(t, telemetry.Options{Endpoint: srv.URL, Interval: time.Hour})

	ctx := context.Background()
	for range 2 {
		if r, err := td.client.ListProjectsWithResponse(ctx, nil); err != nil || r.StatusCode() != http.StatusOK {
			t.Fatalf("list: %v %v", r, err)
		}
	}
	if r, err := td.client.GetProjectWithResponse(ctx, "nope"); err != nil || r.StatusCode() != http.StatusNotFound {
		t.Fatalf("get: %v %v", r, err)
	}
	td.do(t, http.MethodGet, "/v1/projects?state=lost", "")
	td.do(t, http.MethodGet, "/v1/nothing", "")
	td.stop()

	want := map[string]uint64{
		"operation=listProjects status=200":              2,
		"code=not_found operation=getProject status=404": 1,
		// A request the spec does not allow is still its operation's.
		"code=usage operation=listProjects status=400": 1,
		"code=usage status=404":                        1,
	}
	if got := c.requestCounts(t); !maps.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
