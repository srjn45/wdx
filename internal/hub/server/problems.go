package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/srjn45/wdx/internal/hub/account"
	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/hub/node"
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

// userError is a failure of an act on the user named username.
type userError struct {
	username string
	err      error
}

func (e *userError) Error() string { return e.err.Error() + ": " + e.username }
func (e *userError) Unwrap() error { return e.err }

// AuditBrokenError is an audit chain with a row whose hash does not match.
type AuditBrokenError struct {
	Broken audit.Broken
}

func (e *AuditBrokenError) Error() string {
	return fmt.Sprintf("the audit chain is broken at row %d, of %s", e.Broken.ID, e.Broken.At.Format(time.RFC3339))
}

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
	if errors.Is(err, account.ErrNotFound) {
		if e, ok := errors.AsType[*userError](err); ok {
			return hubapi.ErrorCodeNotFound, map[string]any{"username": e.username}
		}
		return hubapi.ErrorCodeNotFound, nil
	}
	if e, ok := errors.AsType[*account.InvalidError](err); ok {
		return hubapi.ErrorCodeInvalidArgument, map[string]any{"argument": e.Field}
	}
	if e, ok := errors.AsType[*RateLimitedError](err); ok {
		return hubapi.ErrorCodeRateLimited, map[string]any{"retry_after_seconds": int((e.Wait + time.Second - 1) / time.Second)}
	}
	if e, ok := errors.AsType[*node.InvalidError](err); ok {
		return hubapi.ErrorCodeInvalidArgument, map[string]any{"argument": e.Field}
	}
	if code, ok := nodeCodes[unwrapNode(err)]; ok {
		if e, ok := errors.AsType[*nodeError](err); ok {
			return code, map[string]any{"node": e.ref}
		}
		return code, nil
	}
	if code, details, ok := docCode(err); ok {
		return code, details
	}
	if e, ok := errors.AsType[*AuditBrokenError](err); ok {
		return hubapi.ErrorCodeAuditBroken, map[string]any{"id": e.Broken.ID, "at": e.Broken.At.Format(time.RFC3339Nano)}
	}
	return hubapi.ErrorCodeInternal, nil
}

// nodeCodes are the codes of the node store's errors.
var nodeCodes = map[error]hubapi.ErrorCode{
	node.ErrLoginNotFound: hubapi.ErrorCodeNotFound,
	node.ErrLoginExpired:  hubapi.ErrorCodeExpiredToken,
	node.ErrLoginDone:     hubapi.ErrorCodeNotFound,
	node.ErrNameTaken:     hubapi.ErrorCodeNameTaken,
	node.ErrWrongVerifier: hubapi.ErrorCodeInvalidGrant,
	node.ErrPending:       hubapi.ErrorCodeAuthorizationPending,
	node.ErrSlowDown:      hubapi.ErrorCodeSlowDown,
	node.ErrDenied:        hubapi.ErrorCodeAccessDenied,
	node.ErrNotFound:      hubapi.ErrorCodeNotFound,
	node.ErrRevoked:       hubapi.ErrorCodeNodeRevoked,
	node.ErrDisabled:      hubapi.ErrorCodeAccountDisabled,
	errStopping:           hubapi.ErrorCodeHubUnavailable,
}

// unwrapNode is the error of nodeCodes err is, or nil.
func unwrapNode(err error) error {
	for e := range nodeCodes {
		if errors.Is(err, e) {
			return e
		}
	}
	return nil
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
