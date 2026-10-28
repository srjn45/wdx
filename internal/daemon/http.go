package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	middleware "github.com/oapi-codegen/nethttp-middleware"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// handler serves the API: every request is logged, checked against the spec,
// and run one at a time when it changes anything.
func (d *Daemon) handler() http.Handler {
	spec, err := api.GetSpec()
	if err != nil {
		panic(fmt.Sprintf("daemon: embedded spec: %v", err))
	}
	// The spec names no servers; requests arrive on the socket with any host.
	spec.Servers = nil
	validate := middleware.OapiRequestValidatorWithOptions(spec, &middleware.Options{
		Options:               openapi3filter.Options{MultiError: false},
		SilenceServersWarning: true,
		ErrorHandlerWithOpts:  invalidRequest,
	})

	strict := api.NewStrictHandlerWithOptions(&server{store: d.store, self: d.Info()}, nil, api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: failed,
	})
	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		panic(fmt.Sprintf("daemon: router: %v", err))
	}
	routes := api.HandlerWithOptions(strict, api.StdHTTPServerOptions{
		BaseRouter:       http.NewServeMux(),
		ErrorHandlerFunc: badRequest,
	})
	return d.logRequests(router, validate(d.oneChangeAtATime(routes)))
}

// invalidRequest reports a request the spec does not allow.
func invalidRequest(ctx context.Context, err error, w http.ResponseWriter, r *http.Request, opts middleware.ErrorHandlerOpts) {
	switch {
	case opts.MatchedRoute == nil:
		err = &requestError{err: fmt.Errorf("no such endpoint: %s %s", r.Method, r.URL.Path), status: http.StatusNotFound}
	case opts.StatusCode < http.StatusInternalServerError:
		// The validator's messages run over many lines; the first says it.
		msg, _, _ := strings.Cut(err.Error(), "\n")
		err = &requestError{err: errors.New(msg)}
	}
	failed(w, r.WithContext(ctx), err)
}

func (d *Daemon) oneChangeAtATime(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			d.changes.Lock()
			defer d.changes.Unlock()
		}
		next.ServeHTTP(w, r)
	})
}

type debugIDKey struct{}

// debugID is the id the request in ctx is logged under.
func debugID(ctx context.Context) string {
	id, _ := ctx.Value(debugIDKey{}).(string)
	return id
}

// logRequests logs every request from what was received to what was
// returned, under the caller's debug id when it sent one, measures it, and
// turns a panic into an internal error.
func (d *Daemon) logRequests(router routers.Router, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get(api.DebugIDHeader)
		if !logging.IsDebugID(id) {
			id = logging.NewDebugID()
		}
		log := d.opts.Log.With("debug_id", id)
		if r.Header.Get(api.LogLevelHeader) == api.LogLevelDebug {
			log = logging.Quiet(log)
		}
		ctx := logging.NewContext(context.WithValue(r.Context(), debugIDKey{}, id), log)
		r = r.WithContext(ctx)

		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Error("request body unreadable", "err", err)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var operation string
		if route, _, err := router.FindRoute(r); err == nil {
			operation = route.Operation.OperationID
		}
		log.Info("request received",
			"operation", operation,
			"method", r.Method,
			"path", r.URL.Path,
			"query", r.URL.RawQuery,
			"client_version", r.Header.Get(api.VersionHeader),
			"body", jsonValue(body))

		w.Header().Set(api.DebugIDHeader, id)
		w.Header().Set(api.VersionHeader, identity.Version)
		rec := &recorder{ResponseWriter: w}
		defer func() {
			var stack []byte
			if v := recover(); v != nil {
				stack = debug.Stack()
				if rec.status == 0 {
					failed(rec, r, fmt.Errorf("panic: %v", v))
				}
			}
			attrs := []any{"method", r.Method, "path", r.URL.Path, "status", rec.status, logging.Duration(start)}
			measured := []attribute.KeyValue{attribute.Int("status", rec.status)}
			if operation != "" {
				measured = append(measured, attribute.String("operation", operation))
			}
			level := slog.LevelInfo
			if rec.status >= http.StatusBadRequest {
				level = slog.LevelError
				code := rec.code()
				attrs = append(attrs, "code", code)
				measured = append(measured, attribute.String("code", string(code)))
			}
			d.requestDuration.Record(context.Background(), time.Since(start).Seconds(), metric.WithAttributes(measured...))
			if stack != nil {
				attrs = append(attrs, "stack", string(stack))
			}
			log.Log(context.Background(), level, "request returned", append(attrs, "response", jsonValue(rec.body.Bytes()))...)
		}()
		next.ServeHTTP(rec, r)
	})
}

// jsonValue logs b as nested JSON when it is JSON, else as a string.
func jsonValue(b []byte) any {
	if json.Valid(b) {
		return json.RawMessage(b)
	}
	return string(b)
}

// recorder keeps the status and body written, for the log.
type recorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

// code is the error code of the problem written.
func (r *recorder) code() api.ErrorCode {
	var p struct {
		Code api.ErrorCode `json:"code"`
	}
	_ = json.Unmarshal(r.body.Bytes(), &p) // no code unless it is a problem
	return p.Code
}

func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}
