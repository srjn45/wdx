package hubcli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// client calls the running hub over its admin socket.
type client struct {
	api  *hubapi.ClientWithResponses
	doer *doer
}

// problemError is an error the hub returned.
type problemError struct {
	problem hubapi.Problem
}

func (e *problemError) Error() string { return e.problem.Detail }

// unavailableError is a hub that cannot be reached.
type unavailableError struct {
	socket string
	err    error
}

func (e *unavailableError) Error() string {
	return fmt.Sprintf("the hub is not answering on %s: %v", e.socket, e.err)
}

func (e *unavailableError) Unwrap() error { return e.err }

// unavailable reports whether err says the hub did not answer.
func unavailable(err error) bool {
	_, ok := errors.AsType[*unavailableError](err)
	return ok
}

// newClient returns a client of the hub listening on socket, whose requests
// the hub logs under debugID, at debug level when quiet.
func newClient(socket, debugID string, quiet bool) *client {
	d := &doer{socket: socket, debugID: debugID, quiet: quiet, http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}}
	// The host is ignored: every request goes to the socket.
	c, err := hubapi.NewClientWithResponses("http://"+name, hubapi.WithHTTPClient(d))
	if err != nil {
		panic(fmt.Sprintf("hubcli: client: %v", err))
	}
	return &client{api: c, doer: d}
}

// doer sends requests with the debug id and version, logs them, and turns a
// failure to reach the hub into an *unavailableError.
type doer struct {
	socket, debugID string
	quiet           bool
	http            *http.Client
	once            sync.Once
}

func (d *doer) Do(req *http.Request) (*http.Response, error) {
	start := time.Now()
	req.Header.Set(hubapi.DebugIDHeader, d.debugID)
	req.Header.Set(hubapi.VersionHeader, identity.Version)
	if d.quiet {
		req.Header.Set(hubapi.LogLevelHeader, hubapi.LogLevelDebug)
	}
	log := logging.FromContext(req.Context())
	resp, err := d.http.Do(req)
	if err != nil {
		log.Info("hub unreachable", "method", req.Method, "path", req.URL.Path, "socket", d.socket, "err", err)
		// "connect: connection refused" says it; the request and address
		// around it do not.
		if op, ok := errors.AsType[*net.OpError](err); ok {
			err = op.Err
		}
		return nil, &unavailableError{socket: d.socket, err: err}
	}
	version := resp.Header.Get(hubapi.VersionHeader)
	log.Info("hub called", "method", req.Method, "path", req.URL.Path, "status", resp.StatusCode, "hub_version", version, logging.Duration(start))
	if version != identity.Version {
		d.once.Do(func() { log.Warn("hub version differs", "hub_version", version, "version", identity.Version) })
	}
	return resp, nil
}

// result returns the first success body set, the problem, or an error for a
// response the spec does not allow.
func result[T any](resp *http.Response, body []byte, problem *hubapi.Problem, ok ...*T) (T, error) {
	var zero T
	for _, v := range ok {
		if v != nil {
			return *v, nil
		}
	}
	if problem != nil {
		return zero, &problemError{problem: *problem}
	}
	return zero, fmt.Errorf("unexpected response from the hub: %s: %s", resp.Status, body)
}

func (c *client) version(ctx context.Context) (hubapi.Version, error) {
	r, err := c.api.GetAdminVersionWithResponse(ctx)
	if err != nil {
		return hubapi.Version{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

func (c *client) reload(ctx context.Context) (hubapi.ReloadResult, error) {
	r, err := c.api.ReloadWithResponse(ctx)
	if err != nil {
		return hubapi.ReloadResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

func (c *client) backup(ctx context.Context, path string) (hubapi.BackupResult, error) {
	r, err := c.api.BackupWithResponse(ctx, hubapi.Backup{Path: path})
	if err != nil {
		return hubapi.BackupResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON201)
}

func (c *client) createInvite(ctx context.Context, expiresSeconds *int64) (hubapi.InviteResult, error) {
	r, err := c.api.CreateInviteWithResponse(ctx, hubapi.CreateInvite{ExpiresSeconds: expiresSeconds})
	if err != nil {
		return hubapi.InviteResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON201)
}

func (c *client) users(ctx context.Context) (hubapi.UserList, error) {
	r, err := c.api.ListUsersWithResponse(ctx)
	if err != nil {
		return hubapi.UserList{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

func (c *client) user(ctx context.Context, username string) (hubapi.UserEnvelope, error) {
	r, err := c.api.GetUserWithResponse(ctx, username)
	if err != nil {
		return hubapi.UserEnvelope{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

func (c *client) updateUser(ctx context.Context, username string, disabled bool) (hubapi.UserResult, error) {
	r, err := c.api.UpdateUserWithResponse(ctx, username, hubapi.UpdateUser{Disabled: disabled})
	if err != nil {
		return hubapi.UserResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

func (c *client) createReset(ctx context.Context, username string, secondFactor bool) (hubapi.ResetResult, error) {
	r, err := c.api.CreateUserResetWithResponse(ctx, username, hubapi.CreateReset{SecondFactor: &secondFactor})
	if err != nil {
		return hubapi.ResetResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON201)
}

func (c *client) removeUser(ctx context.Context, username string) (hubapi.UserResult, error) {
	r, err := c.api.RemoveUserWithResponse(ctx, username)
	if err != nil {
		return hubapi.UserResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

func (c *client) verifyAudit(ctx context.Context) (hubapi.AuditEnvelope, error) {
	r, err := c.api.VerifyAuditWithResponse(ctx)
	if err != nil {
		return hubapi.AuditEnvelope{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}
