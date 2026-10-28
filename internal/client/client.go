// Package client calls the daemon's API over its Unix socket.
package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/srjn45/warden/internal/api"
	"github.com/srjn45/warden/internal/identity"
	"github.com/srjn45/warden/internal/logging"
)

// Client calls the daemon.
type Client struct {
	api *api.ClientWithResponses
}

// Options configure a client.
type Options struct {
	Socket  string
	DebugID string // sent with every request, so the daemon logs under it
	// Quiet asks the daemon to log the requests at debug level.
	Quiet bool
	// VersionMismatch is called once when the daemon's version differs from
	// this binary's.
	VersionMismatch func(daemonVersion string)
}

// ProblemError is an error the daemon returned.
type ProblemError struct {
	Problem api.Problem
}

func (e *ProblemError) Error() string { return e.Problem.Detail }

// UnavailableError is returned when the daemon cannot be reached.
type UnavailableError struct {
	Socket string
	Err    error
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("the daemon is not answering on %s: %v", e.Socket, e.Err)
}

func (e *UnavailableError) Unwrap() error { return e.Err }

// New returns a client of the daemon listening on opts.Socket.
func New(opts Options) *Client {
	d := &doer{opts: opts, http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", opts.Socket)
		},
	}}}
	// The host is ignored: every request goes to the socket.
	c, err := api.NewClientWithResponses("http://"+identity.Name, api.WithHTTPClient(d))
	if err != nil {
		panic(fmt.Sprintf("client: %v", err))
	}
	return &Client{api: c}
}

// doer sends requests with the debug id and version, logs them, and turns a
// failure to reach the daemon into an *UnavailableError.
type doer struct {
	opts    Options
	http    *http.Client
	checked sync.Once
}

func (d *doer) Do(req *http.Request) (*http.Response, error) {
	start := time.Now()
	req.Header.Set(api.DebugIDHeader, d.opts.DebugID)
	req.Header.Set(api.VersionHeader, identity.Version)
	if d.opts.Quiet {
		req.Header.Set(api.LogLevelHeader, api.LogLevelDebug)
	}
	log := logging.FromContext(req.Context())
	resp, err := d.http.Do(req)
	if err != nil {
		log.Info("daemon unreachable", "method", req.Method, "path", req.URL.Path, "socket", d.opts.Socket, "err", err)
		// "connect: connection refused" says it; the request and address
		// around it do not.
		if op, ok := errors.AsType[*net.OpError](err); ok {
			err = op.Err
		}
		return nil, &UnavailableError{Socket: d.opts.Socket, Err: err}
	}
	version := resp.Header.Get(api.VersionHeader)
	log.Info("daemon called", "method", req.Method, "path", req.URL.Path, "status", resp.StatusCode, "daemon_version", version, logging.Duration(start))
	if version != identity.Version && d.opts.VersionMismatch != nil {
		d.checked.Do(func() {
			log.Warn("daemon version differs", "daemon_version", version, "version", identity.Version)
			d.opts.VersionMismatch(version)
		})
	}
	return resp, nil
}

// result returns the first success body set, the problem, or an error for a
// response the spec does not allow.
func result[T any](resp *http.Response, body []byte, problem *api.Problem, ok ...*T) (T, error) {
	var zero T
	for _, v := range ok {
		if v != nil {
			return *v, nil
		}
	}
	if problem != nil {
		return zero, &ProblemError{Problem: *problem}
	}
	return zero, fmt.Errorf("unexpected response from the daemon: %s: %s", resp.Status, body)
}

func (c *Client) Version(ctx context.Context) (api.Version, error) {
	r, err := c.api.GetVersionWithResponse(ctx)
	if err != nil {
		return api.Version{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

func (c *Client) Daemon(ctx context.Context) (api.Daemon, error) {
	r, err := c.api.GetDaemonWithResponse(ctx)
	if err != nil {
		return api.Daemon{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// ListProjects lists the projects in state, or every project when state is
// empty.
func (c *Client) ListProjects(ctx context.Context, state api.ProjectState) (api.ProjectList, error) {
	var params api.ListProjectsParams
	if state != "" {
		params.State = &state
	}
	r, err := c.api.ListProjectsWithResponse(ctx, &params)
	if err != nil {
		return api.ProjectList{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

func (c *Client) GetProject(ctx context.Context, ref string) (api.ProjectEnvelope, error) {
	r, err := c.api.GetProjectWithResponse(ctx, ref)
	if err != nil {
		return api.ProjectEnvelope{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

func (c *Client) CreateProject(ctx context.Context, body api.CreateProject) (api.ProjectResult, error) {
	r, err := c.api.CreateProjectWithResponse(ctx, body)
	if err != nil {
		return api.ProjectResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200, r.JSON201)
}

func (c *Client) UpdateProject(ctx context.Context, ref string, state api.ProjectState) (api.ProjectResult, error) {
	r, err := c.api.UpdateProjectWithResponse(ctx, ref, api.UpdateProject{State: state})
	if err != nil {
		return api.ProjectResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

func (c *Client) DeleteProject(ctx context.Context, ref string) (api.ProjectResult, error) {
	r, err := c.api.DeleteProjectWithResponse(ctx, ref)
	if err != nil {
		return api.ProjectResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

func (c *Client) RecloneProject(ctx context.Context, ref string) (api.ProjectResult, error) {
	r, err := c.api.RecloneProjectWithResponse(ctx, ref)
	if err != nil {
		return api.ProjectResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON201)
}
