package server

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
	"sync"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	middleware "github.com/oapi-codegen/nethttp-middleware"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// endpoint is one of the three the hub serves, named as the spec's tags
// name them.
type endpoint string

const (
	endpointWeb   endpoint = "web"
	endpointNodes endpoint = "nodes"
	endpointAdmin endpoint = "admin"
)

// handler serves the operations of e: every request is logged, refused
// when it is no operation of e, checked against the spec, and on the admin
// endpoint, once valid, run one at a time when it changes anything.
func (s *Server) handler(e endpoint) http.Handler {
	sp := embedded()
	api := &api{server: s, apiVersion: s.apiVersion}
	strict := hubapi.NewStrictHandlerWithOptions(api, nil, hubapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: failed,
	})
	routes := hubapi.HandlerWithOptions(strict, hubapi.StdHTTPServerOptions{
		BaseRouter:       http.NewServeMux(),
		ErrorHandlerFunc: badRequest,
	})
	next := ownOnly(refuseTooLarge(sp.validate(routes)))
	switch e {
	case endpointAdmin:
		next = ownOnly(refuseTooLarge(sp.validate(s.oneChangeAtATime(routes))))
	case endpointNodes:
		// Before anything else, even a 404: the endpoint tells a node it
		// does not know nothing.
		next = s.checkNode(next)
	}
	operations := s.logRequests(e, next)
	if e != endpointWeb {
		return operations
	}
	// The web endpoint serves the sign-in pages beside its operations.
	mux := http.NewServeMux()
	mux.Handle("/v1/", operations)
	mux.Handle("/", s.logPages(s.pages))
	return mux
}

// logPages logs every page request from received to returned with its
// method, path and status alone, never its query, which holds the tokens of
// invite and reset links, nor its form, which holds passwords and codes;
// measures it; and turns a panic into an internal error. A refusal of a page
// is no failure of the hub's: it is logged as returned at info, and each
// step logs why.
func (s *Server) logPages(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := logging.NewDebugID()
		log := s.opts.Log.With("debug_id", id, "endpoint", string(endpointWeb))
		r = r.WithContext(logging.NewContext(context.WithValue(r.Context(), debugIDKey{}, id), log))
		log.Info("request received", "page", true, "method", r.Method, "path", r.URL.Path)
		w.Header().Set(hubapi.DebugIDHeader, id)
		rec := &statusRecorder{ResponseWriter: w}
		defer func() {
			attrs := []any{"page", true, "method", r.Method, "path", r.URL.Path}
			level := slog.LevelInfo
			if v := recover(); v != nil {
				level = slog.LevelError
				attrs = append(attrs, "err", fmt.Sprintf("panic: %v", v), "stack", string(debug.Stack()))
				if rec.status == 0 {
					http.Error(rec, "the page could not be shown", http.StatusInternalServerError)
				}
			}
			if rec.status >= http.StatusInternalServerError {
				level = slog.LevelError
			}
			(*s.requestDuration.Load()).Record(context.Background(), time.Since(start).Seconds(), metric.WithAttributes(
				attribute.String("endpoint", string(endpointWeb)), attribute.Int("status", rec.status)))
			log.Log(context.Background(), level, "request returned", append(attrs, "status", rec.status, logging.Duration(start))...)
		}()
		next.ServeHTTP(rec, r)
	})
}

// statusRecorder keeps the status written, for the log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// spec is what the embedded spec gives: parsed once, and only read after
// that.
type spec struct {
	spec     *openapi3.T
	validate func(http.Handler) http.Handler
	router   routers.Router
	// endpoints names the endpoint of each operation, by its id, from its
	// tag.
	endpoints map[string]endpoint
}

var embedded = sync.OnceValue(func() spec {
	sp, err := hubapi.GetSpec()
	if err != nil {
		panic(fmt.Sprintf("server: embedded spec: %v", err))
	}
	// The spec names no servers; requests arrive with any host.
	sp.Servers = nil
	validate := middleware.OapiRequestValidatorWithOptions(sp, &middleware.Options{
		Options:               openapi3filter.Options{MultiError: false},
		SilenceServersWarning: true,
		ErrorHandlerWithOpts:  invalidRequest,
	})
	router, err := gorillamux.NewRouter(sp)
	if err != nil {
		panic(fmt.Sprintf("server: router: %v", err))
	}
	endpoints := map[string]endpoint{}
	for _, item := range sp.Paths.Map() {
		for _, op := range item.Operations() {
			if len(op.Tags) != 1 {
				panic(fmt.Sprintf("server: operation %s has tags %v, not one endpoint", op.OperationID, op.Tags))
			}
			endpoints[op.OperationID] = endpoint(op.Tags[0])
		}
	}
	return spec{spec: sp, validate: validate, router: router, endpoints: endpoints}
})

// invalidRequest reports a request the spec does not allow.
func invalidRequest(ctx context.Context, err error, w http.ResponseWriter, r *http.Request, opts middleware.ErrorHandlerOpts) {
	if opts.StatusCode < http.StatusInternalServerError {
		// The validator's messages run over many lines; the first says it.
		msg, _, _ := strings.Cut(err.Error(), "\n")
		err = &requestError{err: errors.New(msg)}
	}
	failed(w, r.WithContext(ctx), err)
}

type (
	debugIDKey   struct{}
	operationKey struct{}
	tooLargeKey  struct{}
)

// debugID is the id the request in ctx is logged under.
func debugID(ctx context.Context) string {
	id, _ := ctx.Value(debugIDKey{}).(string)
	return id
}

// ownOnly answers a request for no operation of its endpoint as for a path
// the hub does not have, before it is validated: an endpoint tells nothing of
// the others'.
func ownOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if op, _ := r.Context().Value(operationKey{}).(string); op == "" {
			failed(w, r, &noEndpointError{method: r.Method, path: r.URL.Path})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// refuseTooLarge refuses whole a request whose body is over its operation's
// limit, which logRequests read no further than.
func refuseTooLarge(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tooLarge, _ := r.Context().Value(tooLargeKey{}).(bool); tooLarge {
			op, _ := r.Context().Value(operationKey{}).(string)
			failed(w, r, &requestError{err: fmt.Errorf("the body is over %d bytes, the most %s takes", bodyLimits[op], op)})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) oneChangeAtATime(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		// A change runs to its end when the client leaves: a backup cut
		// short leaves half a file. Only a hub stopping cancels it, once it
		// has run out of time.
		ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
		defer cancel()
		defer context.AfterFunc(s.abort, cancel)()
		if !s.changes.TryLock() {
			logging.FromContext(ctx).Info("request waits for the change in flight")
			s.changes.Lock()
		}
		defer s.changes.Unlock()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// logRequests logs every request of e from what was received to what was
// returned, under the caller's debug id when it sent one, measures it, and
// turns a panic into an internal error.
func (s *Server) logRequests(e endpoint, next http.Handler) http.Handler {
	sp := embedded()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get(hubapi.DebugIDHeader)
		if !logging.IsDebugID(id) {
			id = logging.NewDebugID()
		}
		log := s.opts.Log.With("debug_id", id, "endpoint", string(e))
		if r.Header.Get(hubapi.LogLevelHeader) == hubapi.LogLevelDebug {
			log = logging.Quiet(log)
		}
		// Only an operation of this endpoint is one.
		var operation string
		if route, _, err := sp.router.FindRoute(r); err == nil && sp.endpoints[route.Operation.OperationID] == e {
			operation = route.Operation.OperationID
		}
		body, tooLarge, err := readBody(r.Body, bodyLimits[operation])
		if err != nil {
			log.Error("request body unreadable", "err", err)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		ctx := context.WithValue(context.WithValue(r.Context(), debugIDKey{}, id), operationKey{}, operation)
		ctx = context.WithValue(ctx, tooLargeKey{}, tooLarge)
		ctx = context.WithValue(ctx, addressKey{}, s.pages.Address(r))
		r = r.WithContext(logging.NewContext(ctx, log))
		log.Info("request received",
			"operation", operation,
			"method", r.Method,
			"path", r.URL.Path,
			"query", r.URL.RawQuery,
			"client_version", r.Header.Get(hubapi.VersionHeader),
			"body", requestLogged(operation, body),
			"body_too_large", tooLarge)

		w.Header().Set(hubapi.DebugIDHeader, id)
		w.Header().Set(hubapi.VersionHeader, identity.Version)
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
			measured := []attribute.KeyValue{attribute.String("endpoint", string(e)), attribute.Int("status", rec.status)}
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
			(*s.requestDuration.Load()).Record(context.Background(), time.Since(start).Seconds(), metric.WithAttributes(measured...))
			if stack != nil {
				attrs = append(attrs, "stack", string(stack))
			}
			log.Log(context.Background(), level, "request returned", append(attrs, "response", responseLogged(operation, rec.body.Bytes()))...)
		}()
		next.ServeHTTP(rec, r)
	})
}

// bodyLimits are the most a request's body may hold, by operation id; the
// others' are not limited.
var bodyLimits = map[string]int64{
	"pushDocChanges": maxDocChangesBody,
}

// readBody reads body whole, or up to limit bytes and one more when limit is
// not 0; tooLarge says there was more than limit, and what was read is then
// dropped.
func readBody(body io.Reader, limit int64) (b []byte, tooLarge bool, err error) {
	if limit == 0 {
		b, err = io.ReadAll(body)
		return b, false, err
	}
	b, err = io.ReadAll(io.LimitReader(body, limit+1))
	if int64(len(b)) > limit {
		return nil, true, err
	}
	return b, false, err
}

// requestLogged is a request's body as logged: a setting's value can be a
// secret, as are the codes and the verifier of the node logins, so they are
// measured, never logged.
func requestLogged(operation string, b []byte) any {
	if operation != "updateSetting" {
		return hubapi.RequestLogged(operation, b)
	}
	var r map[string]any
	if json.Unmarshal(b, &r) != nil {
		return map[string]any{"body_bytes": len(b)}
	}
	if value, ok := r["value"].(string); ok {
		delete(r, "value")
		r["value_bytes"] = len(value)
	}
	return r
}

// responseLogged is a response's body as logged: an invite's or a reset's
// link is a secret, as are a device login's codes, so they are measured,
// never logged.
func responseLogged(operation string, b []byte) any {
	if operation == "createInvite" || operation == "createUserReset" {
		return hubapi.LinkResponseLogged(b)
	}
	return hubapi.ResponseLogged(operation, b)
}

// recorder keeps the status and body written, for the log.
type recorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

// code is the error code of the problem written.
func (r *recorder) code() hubapi.ErrorCode {
	var p struct {
		Code hubapi.ErrorCode `json:"code"`
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

// Unwrap is the writer recorded, which the control WebSocket hijacks.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// connectionErrors logs what an http.Server reports of its connections, such
// as a refused handshake, as the hub's lines rather than on stderr.
type connectionErrors struct{ slog.Handler }

func (h connectionErrors) Handle(ctx context.Context, r slog.Record) error {
	line := slog.NewRecord(r.Time, r.Level, "connection failed", r.PC)
	line.AddAttrs(slog.String("err", strings.TrimSpace(r.Message)))
	return h.Handler.Handle(ctx, line)
}
