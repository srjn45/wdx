package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/srjn45/wdx/internal/hubapi"
)

// requestError is a request that cannot be served as sent.
type requestError struct {
	err error
}

func (e *requestError) Error() string { return e.err.Error() }
func (e *requestError) Unwrap() error { return e.err }

// noEndpointError is a request for no operation of the endpoint it reached.
type noEndpointError struct {
	method, path string
}

func (e *noEndpointError) Error() string { return "no such endpoint: " + e.method + " " + e.path }

// PathExistsError is a path something is at already.
type PathExistsError struct {
	Path string
}

func (e *PathExistsError) Error() string { return "something is already at " + e.Path }

// InvalidPathError is a path that cannot be used.
type InvalidPathError struct {
	Path, Reason string
}

func (e *InvalidPathError) Error() string { return e.Path + ": " + e.Reason }

// problemOf returns err as the problem the API reports.
func problemOf(ctx context.Context, err error) hubapi.Problem {
	code, details := classify(err)
	p := hubapi.NewProblem(code, err.Error())
	if details != nil {
		p.Details = &details
	}
	p.DebugId = debugID(ctx)
	return p
}

// classify maps err to its error code and the values it involves.
func classify(err error) (hubapi.ErrorCode, map[string]any) {
	if _, ok := errors.AsType[*requestError](err); ok {
		return hubapi.ErrorCodeUsage, nil
	}
	if _, ok := errors.AsType[*noEndpointError](err); ok {
		return hubapi.ErrorCodeNotFound, nil
	}
	if _, ok := errors.AsType[*ReloadError](err); ok {
		return hubapi.ErrorCodeReloadFailed, nil
	}
	if e, ok := errors.AsType[*UnknownSettingError](err); ok {
		return hubapi.ErrorCodeNotFound, map[string]any{"setting": e.Key}
	}
	if e, ok := errors.AsType[*InvalidSettingError](err); ok {
		return hubapi.ErrorCodeInvalidSetting, map[string]any{"setting": e.Key}
	}
	if e, ok := errors.AsType[*PathExistsError](err); ok {
		return hubapi.ErrorCodePathExists, map[string]any{"path": e.Path}
	}
	if e, ok := errors.AsType[*InvalidPathError](err); ok {
		return hubapi.ErrorCodeInvalidArgument, map[string]any{"argument": "path", "path": e.Path}
	}
	return hubapi.ErrorCodeInternal, nil
}

func writeProblem(w http.ResponseWriter, p hubapi.Problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	json.NewEncoder(w).Encode(p)
}

// failed writes err as a problem; it handles every error the generated
// handlers and the validator report.
func failed(w http.ResponseWriter, r *http.Request, err error) {
	writeProblem(w, problemOf(r.Context(), err))
}

// badRequest writes a request the generated handlers could not decode.
func badRequest(w http.ResponseWriter, r *http.Request, err error) {
	failed(w, r, &requestError{err: err})
}
