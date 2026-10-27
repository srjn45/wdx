// Package telemetry sends the daemon's metrics to an OpenTelemetry collector
// over OTLP/HTTP.
package telemetry

import (
	"context"
	"crypto/tls"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/srjn45/warden/internal/identity"
	"github.com/srjn45/warden/internal/logging"
)

// sendTimeout bounds one send, so a collector that hangs cannot hold up the
// daemon's stop.
const sendTimeout = 10 * time.Second

// Options configure where the metrics go.
type Options struct {
	Endpoint string        // the collector's base URL; nothing is sent when empty
	Interval time.Duration // between two sends
}

// Start returns the provider the daemon's meters come from, and stop, which
// sends what is left and stops sending.
func Start(ctx context.Context, opts Options) (_ metric.MeterProvider, stop func(context.Context) error, _ error) {
	log := logging.FromContext(ctx)
	if opts.Endpoint == "" {
		log.Info("metrics not sent", "reason", "no otel.endpoint")
		return noop.NewMeterProvider(), func(context.Context) error { return nil }, nil
	}
	url := strings.TrimSuffix(opts.Endpoint, "/") + "/v1/metrics"
	// Each option overrides an OTEL_* variable the exporter would read
	// otherwise: wdx takes its settings from config.yaml only.
	exporterOpts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(url),
		otlpmetrichttp.WithHeaders(map[string]string{}),
		otlpmetrichttp.WithCompression(otlpmetrichttp.NoCompression),
		otlpmetrichttp.WithTimeout(sendTimeout),
		otlpmetrichttp.WithTemporalitySelector(sdkmetric.DefaultTemporalitySelector),
		otlpmetrichttp.WithAggregationSelector(sdkmetric.DefaultAggregationSelector),
	}
	if strings.HasPrefix(url, "https:") {
		exporterOpts = append(exporterOpts, otlpmetrichttp.WithTLSClientConfig(&tls.Config{}))
	}
	exporter, err := otlpmetrichttp.New(ctx, exporterOpts...)
	if err != nil {
		return nil, nil, err
	}
	reader := sdkmetric.NewPeriodicReader(&reporting{Exporter: exporter, log: log, url: url},
		sdkmetric.WithInterval(opts.Interval),
		sdkmetric.WithTimeout(sendTimeout))
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithExemplarFilter(exemplar.TraceBasedFilter),
		sdkmetric.WithResource(resource.NewSchemaless(
			attribute.String("service.name", identity.Name),
			attribute.String("service.version", identity.Version))))
	log.Info("metrics exporter started", "url", url, "interval_ms", opts.Interval.Milliseconds())
	return provider, provider.Shutdown, nil
}

// reporting logs when sending starts failing and when it works again,
// instead of the SDK printing every failure to stderr. Counts are
// cumulative, so the next send that gets through carries what was missed.
type reporting struct {
	sdkmetric.Exporter
	log     *slog.Logger
	url     string
	failing atomic.Bool
}

func (r *reporting) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	err := r.Exporter.Export(ctx, rm)
	switch {
	case err != nil && !r.failing.Swap(true):
		// Info, not warn: without wdx observe running, nothing listens.
		r.log.Info("metrics could not be sent", "url", r.url, "err", err)
	case err == nil && r.failing.Swap(false):
		r.log.Info("metrics sent again", "url", r.url)
	}
	return nil
}
