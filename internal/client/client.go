// Package client calls the daemon's API over its Unix socket.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// Client calls the daemon.
type Client struct {
	api  *api.ClientWithResponses
	doer *doer
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
	// Dial, when set, opens each connection in place of Socket: a stream to
	// Node, another node's daemon, which the client speaks to through it.
	// Its errors come back as they are, so they may carry their own hints.
	Dial func(ctx context.Context) (net.Conn, error)
	// Node is the node Dial reaches, for the log and the problems.
	Node string
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

// nodeIdleConn is how long a stream to another node stays open unused
// between two requests: less than the host's session server waits for the
// next request, so that a request never goes on a stream the host is closing.
const nodeIdleConn = 5 * time.Second

// New returns a client of the daemon listening on opts.Socket, or of the
// node opts.Dial reaches.
func New(opts Options) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", opts.Socket)
		},
	}
	if opts.Dial != nil {
		// A request that finds every stream busy opens another: a stream of
		// events holds its own for as long as it is followed, as TIDE's does.
		// One kept idle serves the next request without a handshake between
		// the nodes. Streams open one at a time, so that a dial that opens
		// the session, asking for the code, does it once for all of them.
		var dialing sync.Mutex
		transport = &http.Transport{MaxIdleConnsPerHost: 1, IdleConnTimeout: nodeIdleConn,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				dialing.Lock()
				defer dialing.Unlock()
				conn, err := opts.Dial(ctx)
				if err != nil {
					return nil, &dialError{err}
				}
				return conn, nil
			},
		}
	}
	d := &doer{opts: opts, http: &http.Client{Transport: transport}}
	// The host is ignored: every request goes to the socket.
	c, err := api.NewClientWithResponses("http://"+identity.Name, api.WithHTTPClient(d))
	if err != nil {
		panic(fmt.Sprintf("client: %v", err))
	}
	return &Client{api: c, doer: d}
}

// Wait waits for the requests in flight to be answered or to fail, each logging
// how: a command ending with requests in flight, as tide does with the ones it
// sends in the background, cancels them and waits, so that the log says what
// became of every one.
func (c *Client) Wait() { c.doer.inflight.Wait() }

// Close closes the connections kept open between requests: with Dial, the
// streams to the node.
func (c *Client) Close() { c.doer.http.CloseIdleConnections() }

// APIVersion is the version of the API the daemon last answered with, 0
// before any answer or from a daemon that does not say it.
func (c *Client) APIVersion() int { return int(c.doer.apiVersion.Load()) }

// dialError is a failure of Options.Dial, which the client returns as it is.
type dialError struct{ err error }

func (e *dialError) Error() string { return e.err.Error() }
func (e *dialError) Unwrap() error { return e.err }

// doer sends requests with the debug id and version, logs them, and turns a
// failure to reach the daemon into an *UnavailableError.
type doer struct {
	opts       Options
	http       *http.Client
	checked    sync.Once
	inflight   sync.WaitGroup
	apiVersion atomic.Int64
}

func (d *doer) Do(req *http.Request) (*http.Response, error) {
	d.inflight.Add(1)
	defer d.inflight.Done()
	start := time.Now()
	req.Header.Set(api.DebugIDHeader, d.opts.DebugID)
	req.Header.Set(api.VersionHeader, identity.Version)
	if d.opts.Quiet {
		req.Header.Set(api.LogLevelHeader, api.LogLevelDebug)
	}
	log := logging.FromContext(req.Context())
	if d.opts.Dial != nil {
		return d.doNode(req, start)
	}
	resp, err := d.http.Do(req) //nolint:gosec // the daemon's socket, or the node --node names through it
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
	d.recordAPIVersion(resp)
	log.Info("daemon called", "method", req.Method, "path", req.URL.Path, "status", resp.StatusCode, "daemon_version", version, logging.Duration(start))
	if version != identity.Version && d.opts.VersionMismatch != nil {
		d.checked.Do(func() {
			log.Warn("daemon version differs", "daemon_version", version, "version", identity.Version)
			d.opts.VersionMismatch(version)
		})
	}
	return resp, nil
}

// doNode sends req to the node Dial reaches, with this build's API version
// and the oldest it speaks, which a host too old for it refuses before any
// handler, and refuses an answer from a host older than this build still
// speaks to, whose release did not check that. A
// stream cut after the dial, before or while the answer is read, is
// connection_lost: the session may still be open, and the request may have
// run. Only the dial knows whether the session closed, and says so. No
// version warning: two machines run different releases often, and the API
// version is what they must agree on.
func (d *doer) doNode(req *http.Request, start time.Time) (*http.Response, error) {
	log := logging.FromContext(req.Context())
	node := d.opts.Node
	req.Header.Set(api.APIVersionHeader, strconv.Itoa(api.CurrentVersion))
	req.Header.Set(api.APIOldestHeader, strconv.Itoa(api.OldestCompatible))
	resp, err := d.http.Do(req) //nolint:gosec // the daemon's socket
	if err != nil {
		log.Info("node unreachable", "method", req.Method, "path", req.URL.Path, "node", node, "err", err)
		if dial, ok := errors.AsType[*dialError](err); ok {
			return nil, dial.err
		}
		if req.Context().Err() != nil {
			return nil, err
		}
		return nil, d.cut(err)
	}
	version := d.recordAPIVersion(resp)
	log.Info("node called", "method", req.Method, "path", req.URL.Path, "status", resp.StatusCode, "node", node, "api_version", version, logging.Duration(start))
	if version < api.OldestCompatible {
		resp.Body.Close()
		log.Warn("node api version unsupported", "node", node, "api_version", version, "oldest_compatible", api.OldestCompatible)
		return nil, d.hostTooOld(version)
	}
	// A stream of events that ends is a stream ending, which its reader
	// takes as it is.
	if mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mediaType != "text/event-stream" {
		resp.Body = &nodeBody{ReadCloser: resp.Body, req: req, d: d}
	}
	return resp, nil
}

// cut is the problem of a stream to the node cut after the dial, by err.
func (d *doer) cut(err error) error {
	p := api.NewProblem(api.ErrorCodeConnectionLost, fmt.Sprintf("the stream to node %s ended before it answered: %v", d.opts.Node, err))
	p.Details = &map[string]any{"node": d.opts.Node}
	return &ProblemError{Problem: p}
}

// nodeBody is the body of an answer from the node, whose stream cut while
// it is read is connection_lost, as one cut before the headers: whichever
// generated parser reads it.
type nodeBody struct {
	io.ReadCloser
	req *http.Request
	d   *doer
}

func (b *nodeBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && b.req.Context().Err() == nil {
		logging.FromContext(b.req.Context()).Info("node stream cut", "method", b.req.Method, "path", b.req.URL.Path, "node", b.d.opts.Node, "err", err)
		return n, b.d.cut(err)
	}
	return n, err
}

// hostTooOld is the problem of a host that speaks API version, older than
// the oldest this build speaks.
func (d *doer) hostTooOld(version int) error {
	node := d.opts.Node
	p := api.NewProblem(api.ErrorCodeNodeVersionUnsupported, fmt.Sprintf("node %s speaks API version %d, older than %d, the oldest this wdx speaks: update wdx on %s", node, version, api.OldestCompatible, node))
	p.Details = &map[string]any{"update": "host", "node": node, "host_version": version, "oldest_compatible": api.OldestCompatible}
	return &ProblemError{Problem: p}
}

// recordAPIVersion keeps the API version resp says, 0 when it says none,
// and returns it.
func (d *doer) recordAPIVersion(resp *http.Response) int {
	version, err := strconv.Atoi(resp.Header.Get(api.APIVersionHeader))
	if err != nil {
		version = 0
	}
	d.apiVersion.Store(int64(version))
	return version
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
// ReloadDaemon has the daemon read the settings again and apply them.
func (c *Client) ReloadDaemon(ctx context.Context) error {
	r, err := c.api.ReloadDaemonWithResponse(ctx)
	if err != nil {
		return err
	}
	_, err = result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
	return err
}

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

// UpdateProject opens or closes a project; closing one with live terminals
// stops them when stopRunning, and is refused otherwise.
func (c *Client) UpdateProject(ctx context.Context, ref string, state api.ProjectState, stopRunning bool) (api.ProjectResult, error) {
	body := api.UpdateProject{State: state}
	if stopRunning {
		body.StopRunning = &stopRunning
	}
	r, err := c.api.UpdateProjectWithResponse(ctx, ref, body)
	if err != nil {
		return api.ProjectResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// DeleteProject removes a project; one with live terminals stops them when
// stopRunning, and is refused otherwise.
func (c *Client) DeleteProject(ctx context.Context, ref string, stopRunning bool) (api.ProjectResult, error) {
	var params api.DeleteProjectParams
	if stopRunning {
		params.StopRunning = &stopRunning
	}
	r, err := c.api.DeleteProjectWithResponse(ctx, ref, &params)
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

// ListAgents lists the agents of the project named by project, or of the one
// whose worktree holds dir, or of every project.
func (c *Client) ListAgents(ctx context.Context, project, dir string) (api.AgentList, error) {
	r, err := c.api.ListAgentsWithResponse(ctx, &api.ListAgentsParams{Project: nonEmpty(project), Dir: nonEmpty(dir)})
	if err != nil {
		return api.AgentList{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// ListAcas lists the AI coding agents new agents can run, the default first.
func (c *Client) ListAcas(ctx context.Context) (api.AcaList, error) {
	r, err := c.api.ListAcasWithResponse(ctx)
	if err != nil {
		return api.AcaList{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// ListUsage returns how much of each AI coding agent's account's limits is used, a
// row per ACA, window and bucket.
func (c *Client) ListUsage(ctx context.Context) (api.UsageList, error) {
	r, err := c.api.ListUsageWithResponse(ctx)
	if err != nil {
		return api.UsageList{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// ListAcaModels returns the models the AI coding agent with id offers.
func (c *Client) ListAcaModels(ctx context.Context, id string) (api.AcaModelList, error) {
	r, err := c.api.ListAcaModelsWithResponse(ctx, id)
	if err != nil {
		return api.AcaModelList{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// CreateAgent creates an agent and starts its AI coding agent.
func (c *Client) CreateAgent(ctx context.Context, body api.CreateAgent) (api.AgentResult, error) {
	r, err := c.api.CreateAgentWithResponse(ctx, body)
	if err != nil {
		return api.AgentResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON201)
}

// GetAgent returns the agent ref names, as StartAgent does.
func (c *Client) GetAgent(ctx context.Context, ref, project, dir string) (api.AgentEnvelope, error) {
	r, err := c.api.GetAgentWithResponse(ctx, ref, &api.GetAgentParams{Project: nonEmpty(project), Dir: nonEmpty(dir)})
	if err != nil {
		return api.AgentEnvelope{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// GetAgentHistory reads the history of the agent ref names, as GetAgent does:
// count lines from line from on, or the last count lines when from is nil.
func (c *Client) GetAgentHistory(ctx context.Context, ref, project, dir string, from *int64, count int) (api.TerminalHistoryEnvelope, error) {
	params := api.GetAgentHistoryParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}
	if from != nil {
		params.From, params.Count = from, &count
	} else {
		params.Tail = &count
	}
	r, err := c.api.GetAgentHistoryWithResponse(ctx, ref, &params)
	if err != nil {
		return api.TerminalHistoryEnvelope{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// GetAgentTranscript reads the conversation of the agent ref names as text, as
// GetAgent does: count lines from line from on, or the last count lines when
// from is nil.
func (c *Client) GetAgentTranscript(ctx context.Context, ref, project, dir string, from *int64, count int) (api.AgentTranscriptEnvelope, error) {
	params := api.GetAgentTranscriptParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}
	if from != nil {
		params.From, params.Count = from, &count
	} else {
		params.Tail = &count
	}
	r, err := c.api.GetAgentTranscriptWithResponse(ctx, ref, &params)
	if err != nil {
		return api.AgentTranscriptEnvelope{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// GetAgentBrief reads the conversation of the agent ref names in brief, as GetAgent does.
func (c *Client) GetAgentBrief(ctx context.Context, ref, project, dir string) (api.AgentTranscriptEnvelope, error) {
	brief := true
	r, err := c.api.GetAgentTranscriptWithResponse(ctx, ref, &api.GetAgentTranscriptParams{Project: nonEmpty(project), Dir: nonEmpty(dir), Brief: &brief})
	if err != nil {
		return api.AgentTranscriptEnvelope{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// RenameAgent renames the agent ref names, as StartAgent does.
func (c *Client) RenameAgent(ctx context.Context, ref, project, dir, name string) (api.AgentResult, error) {
	r, err := c.api.UpdateAgentWithResponse(ctx, ref, &api.UpdateAgentParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}, api.UpdateAgent{Name: name})
	if err != nil {
		return api.AgentResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// StartAgent starts the agent ref names: "id:" and its id, or its name in the
// project named by project or holding dir.
func (c *Client) StartAgent(ctx context.Context, ref, project, dir string) (api.AgentResult, error) {
	r, err := c.api.StartAgentWithResponse(ctx, ref, &api.StartAgentParams{Project: nonEmpty(project), Dir: nonEmpty(dir)})
	if err != nil {
		return api.AgentResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// RestartAgent starts the agent ref names in a new session, as StartAgent does; force
// does so in the middle of its work.
func (c *Client) RestartAgent(ctx context.Context, ref, project, dir string, force bool) (api.AgentResult, error) {
	r, err := c.api.RestartAgentWithResponse(ctx, ref, &api.RestartAgentParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}, api.RestartAgent{Force: &force})
	if err != nil {
		return api.AgentResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// SendAgent queues a message for the agent ref names, as StartAgent does;
// instant says it may interrupt the agent's turn.
func (c *Client) SendAgent(ctx context.Context, ref, project, dir, prompt string, instant bool) (api.AgentResult, error) {
	message := api.AgentMessage{Prompt: prompt}
	if instant {
		message.Instant = &instant
	}
	r, err := c.api.SendAgentWithResponse(ctx, ref, &api.SendAgentParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}, message)
	if err != nil {
		return api.AgentResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// DeleteAgent removes the agent ref names, as StartAgent does; a live one is
// refused unless stopRunning.
func (c *Client) DeleteAgent(ctx context.Context, ref, project, dir string, stopRunning bool) (api.AgentResult, error) {
	params := api.DeleteAgentParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}
	if stopRunning {
		params.StopRunning = &stopRunning
	}
	r, err := c.api.DeleteAgentWithResponse(ctx, ref, &params)
	if err != nil {
		return api.AgentResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// ListSettings returns every setting.
func (c *Client) ListSettings(ctx context.Context) (api.SettingList, error) {
	r, err := c.api.ListSettingsWithResponse(ctx)
	if err != nil {
		return api.SettingList{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// SetSetting changes the setting key, and the daemon applies it.
func (c *Client) SetSetting(ctx context.Context, key, value string) (api.SettingResult, error) {
	r, err := c.api.UpdateSettingWithResponse(ctx, key, api.UpdateSetting{Value: value})
	if err != nil {
		return api.SettingResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// ListTerminals lists the terminals of the project named by project, or of
// the one holding dir, or of every project when both are empty.
// ListWorktrees reads every checkout of the project named by ref or holding dir.
func (c *Client) ListWorktrees(ctx context.Context, ref, dir string) (api.WorktreeList, error) {
	r, err := c.api.ListWorktreesWithResponse(ctx, &api.ListWorktreesParams{Project: nonEmpty(ref), Dir: nonEmpty(dir)})
	if err != nil {
		return api.WorktreeList{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// LandWorktree starts landing the worktree name: its status says how it ends.
// A method of "" is what the project says; agentID, when not "", is the agent
// asking, not counted as working in it.
func (c *Client) LandWorktree(ctx context.Context, name, ref, dir, method, agentID string) (api.WorktreeLandResult, error) {
	params := &api.LandWorktreeParams{Project: nonEmpty(ref), Dir: nonEmpty(dir), AgentId: nonEmpty(agentID)}
	if method != "" {
		m := api.LandWorktreeParamsMethod(method)
		params.Method = &m
	}
	r, err := c.api.LandWorktreeWithResponse(ctx, name, params)
	if err != nil {
		return api.WorktreeLandResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON202)
}

// ProjectLanding returns how the project ref lands its worktrees.
func (c *Client) ProjectLanding(ctx context.Context, ref string) (api.ProjectLanding, error) {
	r, err := c.api.GetProjectLandingWithResponse(ctx, ref)
	if err != nil {
		return api.ProjectLanding{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// SetProjectLanding says how the project ref lands its worktrees.
func (c *Client) SetProjectLanding(ctx context.Context, ref string, body api.ProjectLandingUpdate) (api.ProjectLanding, error) {
	r, err := c.api.SetProjectLandingWithResponse(ctx, ref, body)
	if err != nil {
		return api.ProjectLanding{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// RemoveWorktree removes the worktree name, which must have landed.
func (c *Client) RemoveWorktree(ctx context.Context, name, ref, dir string) (api.WorktreeRemoved, error) {
	r, err := c.api.RemoveWorktreeWithResponse(ctx, name, &api.RemoveWorktreeParams{Project: nonEmpty(ref), Dir: nonEmpty(dir)})
	if err != nil {
		return api.WorktreeRemoved{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// RebaseWorktree rebases the worktree name onto its base; agentID, when not "",
// is the agent asking, not counted as working in it.
func (c *Client) RebaseWorktree(ctx context.Context, name, ref, dir, agentID string) (api.WorktreeRebased, error) {
	r, err := c.api.RebaseWorktreeWithResponse(ctx, name, &api.RebaseWorktreeParams{Project: nonEmpty(ref), Dir: nonEmpty(dir), AgentId: nonEmpty(agentID)})
	if err != nil {
		return api.WorktreeRebased{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// WorktreeDiff returns what the worktree name has committed since it left its base.
func (c *Client) WorktreeDiff(ctx context.Context, name, ref, dir string) (api.WorktreeDiff, error) {
	r, err := c.api.GetWorktreeDiffWithResponse(ctx, name, &api.GetWorktreeDiffParams{Project: nonEmpty(ref), Dir: nonEmpty(dir)})
	if err != nil {
		return api.WorktreeDiff{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// WorktreeUncommittedDiff returns everything the worktree name has not committed.
func (c *Client) WorktreeUncommittedDiff(ctx context.Context, name, ref, dir string) (api.WorktreeDiff, error) {
	on := true
	r, err := c.api.GetWorktreeDiffWithResponse(ctx, name, &api.GetWorktreeDiffParams{Project: nonEmpty(ref), Dir: nonEmpty(dir), Uncommitted: &on})
	if err != nil {
		return api.WorktreeDiff{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// WorktreeFiles returns the files of the worktree name, without gitignored ones.
func (c *Client) WorktreeFiles(ctx context.Context, name, ref, dir string) (api.WorktreeFiles, error) {
	r, err := c.api.ListWorktreeFilesWithResponse(ctx, name, &api.ListWorktreeFilesParams{Project: nonEmpty(ref), Dir: nonEmpty(dir)})
	if err != nil {
		return api.WorktreeFiles{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// CreateWorktreeFile creates an empty file, or a directory, at path of the worktree
// name.
func (c *Client) CreateWorktreeFile(ctx context.Context, name, path string, directory bool, ref string) (api.WorktreeFileChange, error) {
	r, err := c.api.CreateWorktreeFileWithResponse(ctx, name, &api.CreateWorktreeFileParams{Project: nonEmpty(ref)}, api.WorktreeFileCreate{Path: path, Directory: &directory})
	if err != nil {
		return api.WorktreeFileChange{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON201)
}

// RenameWorktreeFile gives the file or directory at path of the worktree name the
// name newName.
func (c *Client) RenameWorktreeFile(ctx context.Context, name, path, newName, ref string) (api.WorktreeFileChange, error) {
	r, err := c.api.RenameWorktreeFileWithResponse(ctx, name, &api.RenameWorktreeFileParams{Project: nonEmpty(ref)}, api.WorktreeFileRename{Path: path, Name: newName})
	if err != nil {
		return api.WorktreeFileChange{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// RemoveWorktreeFile deletes the file, or the directory, at path of the worktree
// name.
func (c *Client) RemoveWorktreeFile(ctx context.Context, name, path, ref string) (api.WorktreeFileChange, error) {
	r, err := c.api.RemoveWorktreeFileWithResponse(ctx, name, &api.RemoveWorktreeFileParams{Path: path, Project: nonEmpty(ref)})
	if err != nil {
		return api.WorktreeFileChange{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// WorktreeFileContent returns what the file at path of the worktree name holds now.
func (c *Client) WorktreeFileContent(ctx context.Context, name, path, ref, dir string) (api.WorktreeFileContent, error) {
	r, err := c.api.GetWorktreeFileContentWithResponse(ctx, name, &api.GetWorktreeFileContentParams{Path: path, Project: nonEmpty(ref), Dir: nonEmpty(dir)})
	if err != nil {
		return api.WorktreeFileContent{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// OpenEditor starts the editor the editor setting names on the file at path of the
// worktree name, or returns the one running on it.
func (c *Client) OpenEditor(ctx context.Context, name, path, ref string) (api.EditorResult, error) {
	r, err := c.api.OpenEditorWithResponse(ctx, api.OpenEditor{Worktree: name, Path: path, Project: nonEmpty(ref)})
	if err != nil {
		return api.EditorResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200, r.JSON201)
}

// WorktreeFileDiff returns the uncommitted changes of the file at path in the
// worktree name.
func (c *Client) WorktreeFileDiff(ctx context.Context, name, path, ref, dir string) (api.WorktreeFileDiff, error) {
	r, err := c.api.GetWorktreeFileDiffWithResponse(ctx, name, &api.GetWorktreeFileDiffParams{Path: path, Project: nonEmpty(ref), Dir: nonEmpty(dir)})
	if err != nil {
		return api.WorktreeFileDiff{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// VerifyWorktreeFile marks the file at path of the worktree name verified, with the
// content seen as contentID, or as it is now when that is "".
func (c *Client) VerifyWorktreeFile(ctx context.Context, name, path, contentID, ref string) error {
	r, err := c.api.VerifyWorktreeFileWithResponse(ctx, name, &api.VerifyWorktreeFileParams{Path: path, Project: nonEmpty(ref)}, api.WorktreeFileVerify{ContentId: nonEmpty(contentID)})
	if err != nil {
		return err
	}
	_, err = result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
	return err
}

// UnverifyWorktreeFile takes the mark of the file at path of the worktree name away.
func (c *Client) UnverifyWorktreeFile(ctx context.Context, name, path, ref string) error {
	r, err := c.api.UnverifyWorktreeFileWithResponse(ctx, name, &api.UnverifyWorktreeFileParams{Path: path, Project: nonEmpty(ref)})
	if err != nil {
		return err
	}
	_, err = result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
	return err
}

// WorktreeComments lists the comments of the worktree name, in every state.
func (c *Client) WorktreeComments(ctx context.Context, name, ref, dir string) (api.WorktreeCommentList, error) {
	r, err := c.api.ListWorktreeCommentsWithResponse(ctx, name, &api.ListWorktreeCommentsParams{Project: nonEmpty(ref), Dir: nonEmpty(dir)})
	if err != nil {
		return api.WorktreeCommentList{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// AddWorktreeComment saves a comment on a line of a diff of the worktree name.
func (c *Client) AddWorktreeComment(ctx context.Context, name, ref string, body api.WorktreeCommentCreate) error {
	r, err := c.api.CreateWorktreeCommentWithResponse(ctx, name, &api.CreateWorktreeCommentParams{Project: nonEmpty(ref)}, body)
	if err != nil {
		return err
	}
	if r.StatusCode() == http.StatusCreated {
		return nil
	}
	_, err = result[struct{}](r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault)
	return err
}

// EditWorktreeComment changes the text of a comment not sent yet.
func (c *Client) EditWorktreeComment(ctx context.Context, name, id, ref, body string) error {
	r, err := c.api.EditWorktreeCommentWithResponse(ctx, name, id, &api.EditWorktreeCommentParams{Project: nonEmpty(ref)}, api.WorktreeCommentEdit{Body: body})
	if err != nil {
		return err
	}
	if r.StatusCode() == http.StatusOK {
		return nil
	}
	_, err = result[struct{}](r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault)
	return err
}

// DeleteWorktreeComment deletes a comment not sent yet.
func (c *Client) DeleteWorktreeComment(ctx context.Context, name, id, ref string) error {
	r, err := c.api.DeleteWorktreeCommentWithResponse(ctx, name, id, &api.DeleteWorktreeCommentParams{Project: nonEmpty(ref)})
	if err != nil {
		return err
	}
	if r.StatusCode() == http.StatusOK {
		return nil
	}
	_, err = result[struct{}](r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault)
	return err
}

// SendWorktreeReview sends the comments of the worktree name not sent yet to agent.
func (c *Client) SendWorktreeReview(ctx context.Context, name, ref, agent, note string) (api.WorktreeReviewResult, error) {
	r, err := c.api.SendWorktreeReviewWithResponse(ctx, name, &api.SendWorktreeReviewParams{Project: nonEmpty(ref)}, api.WorktreeReview{Agent: agent, Note: nonEmpty(note)})
	if err != nil {
		return api.WorktreeReviewResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// ListMessages lists the messages still waiting in the project named by ref or
// holding dir.
func (c *Client) ListMessages(ctx context.Context, ref, dir string) (api.MessageList, error) {
	r, err := c.api.ListMessagesWithResponse(ctx, &api.ListMessagesParams{Project: nonEmpty(ref), Dir: nonEmpty(dir)})
	if err != nil {
		return api.MessageList{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// ResendMessage sends a message still waiting.
func (c *Client) ResendMessage(ctx context.Context, id string) (api.MessageResult, error) {
	r, err := c.api.ResendMessageWithResponse(ctx, id)
	if err != nil {
		return api.MessageResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// DiscardMessage discards a message still waiting.
func (c *Client) DiscardMessage(ctx context.Context, id string) (api.MessageDiscarded, error) {
	r, err := c.api.DiscardMessageWithResponse(ctx, id)
	if err != nil {
		return api.MessageDiscarded{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// RefreshWorktrees asks for the worktree name of the project ref to be read
// again, or all of them when name is "".
func (c *Client) RefreshWorktrees(ctx context.Context, ref, name string, pullRequests bool) error {
	params := &api.RefreshWorktreesParams{Worktree: nonEmpty(name)}
	if pullRequests {
		params.PullRequests = &pullRequests
	}
	r, err := c.api.RefreshWorktreesWithResponse(ctx, ref, params)
	if err != nil {
		return err
	}
	if r.StatusCode() == http.StatusAccepted {
		return nil
	}
	_, err = result[struct{}](r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault)
	return err
}

func (c *Client) ListTerminals(ctx context.Context, project, dir string) (api.TerminalList, error) {
	r, err := c.api.ListTerminalsWithResponse(ctx, &api.ListTerminalsParams{Project: nonEmpty(project), Dir: nonEmpty(dir)})
	if err != nil {
		return api.TerminalList{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

func (c *Client) CreateTerminal(ctx context.Context, body api.CreateTerminal) (api.TerminalResult, error) {
	r, err := c.api.CreateTerminalWithResponse(ctx, body)
	if err != nil {
		return api.TerminalResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON201)
}

// GetTerminal returns the terminal ref names, as StartTerminal does.
func (c *Client) GetTerminal(ctx context.Context, ref, project, dir string) (api.TerminalEnvelope, error) {
	r, err := c.api.GetTerminalWithResponse(ctx, ref, &api.GetTerminalParams{Project: nonEmpty(project), Dir: nonEmpty(dir)})
	if err != nil {
		return api.TerminalEnvelope{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// GetTerminalHistory reads the history of the terminal ref names, as
// GetTerminal does: count lines from line from on, or the last count lines
// when from is nil.
func (c *Client) GetTerminalHistory(ctx context.Context, ref, project, dir string, from *int64, count int) (api.TerminalHistoryEnvelope, error) {
	params := api.GetTerminalHistoryParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}
	if from != nil {
		params.From, params.Count = from, &count
	} else {
		params.Tail = &count
	}
	r, err := c.api.GetTerminalHistoryWithResponse(ctx, ref, &params)
	if err != nil {
		return api.TerminalHistoryEnvelope{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// StartTerminal starts the terminal ref names: "id:" and its id, or its
// name in the project named by project or holding dir.
func (c *Client) StartTerminal(ctx context.Context, ref, project, dir string) (api.TerminalResult, error) {
	r, err := c.api.StartTerminalWithResponse(ctx, ref, &api.StartTerminalParams{Project: nonEmpty(project), Dir: nonEmpty(dir)})
	if err != nil {
		return api.TerminalResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// SendTerminal queues prompt for the terminal ref names, as StartTerminal does.
func (c *Client) SendTerminal(ctx context.Context, ref, project, dir, prompt string) (api.TerminalResult, error) {
	r, err := c.api.SendTerminalWithResponse(ctx, ref, &api.SendTerminalParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}, api.TerminalMessage{Prompt: prompt})
	if err != nil {
		return api.TerminalResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// DeleteTerminal removes the terminal ref names, as StartTerminal does; a
// live one is stopped when stopRunning, and refused otherwise.
func (c *Client) DeleteTerminal(ctx context.Context, ref, project, dir string, stopRunning bool) (api.TerminalResult, error) {
	params := api.DeleteTerminalParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}
	if stopRunning {
		params.StopRunning = &stopRunning
	}
	r, err := c.api.DeleteTerminalWithResponse(ctx, ref, &params)
	if err != nil {
		return api.TerminalResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// nonEmpty is nil for "", a pointer to s otherwise.
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ListDocs lists the docs of the project named by project or holding dir, those
// with every one of labels and in status unless it is empty.
func (c *Client) ListDocs(ctx context.Context, project, dir string, labels []string, status api.DocStatus) (api.DocList, error) {
	params := api.ListDocsParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}
	if len(labels) > 0 {
		params.Labels = &labels
	}
	if status != "" {
		params.Status = &status
	}
	r, err := c.api.ListDocsWithResponse(ctx, &params)
	if err != nil {
		return api.DocList{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

func (c *Client) CreateDoc(ctx context.Context, body api.CreateDoc) (api.DocResult, error) {
	r, err := c.api.CreateDocWithResponse(ctx, body)
	if err != nil {
		return api.DocResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON201)
}

// GetDoc returns the doc ref names, "id:" and its id or its name in the project
// named by project or holding dir, with revision's content, the latest when it is
// 0.
func (c *Client) GetDoc(ctx context.Context, ref, project, dir string, revision int64) (api.DocContent, error) {
	params := api.GetDocParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}
	if revision != 0 {
		params.Revision = &revision
	}
	r, err := c.api.GetDocWithResponse(ctx, ref, &params)
	if err != nil {
		return api.DocContent{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// UpdateDoc changes the doc ref names, as GetDoc does, as body says.
func (c *Client) UpdateDoc(ctx context.Context, ref, project, dir string, body api.UpdateDoc) (api.DocResult, error) {
	r, err := c.api.UpdateDocWithResponse(ctx, ref, &api.UpdateDocParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}, body)
	if err != nil {
		return api.DocResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// UpdateDocContent writes content as the next revision of the doc ref names, as
// GetDoc does, if base is still its latest.
func (c *Client) UpdateDocContent(ctx context.Context, ref, project, dir string, base int64, content string) (api.DocResult, error) {
	r, err := c.api.UpdateDocContentWithResponse(ctx, ref, &api.UpdateDocContentParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}, api.DocContentChange{Base: base, Content: content})
	if err != nil {
		return api.DocResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// ListDocRevisions returns the revisions of the doc ref names, as GetDoc does.
func (c *Client) ListDocRevisions(ctx context.Context, ref, project, dir string) (api.DocRevisionList, error) {
	r, err := c.api.ListDocRevisionsWithResponse(ctx, ref, &api.ListDocRevisionsParams{Project: nonEmpty(project), Dir: nonEmpty(dir)})
	if err != nil {
		return api.DocRevisionList{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// DeleteDoc removes the doc ref names, as GetDoc does.
func (c *Client) DeleteDoc(ctx context.Context, ref, project, dir string) (api.DocResult, error) {
	r, err := c.api.DeleteDocWithResponse(ctx, ref, &api.DeleteDocParams{Project: nonEmpty(project), Dir: nonEmpty(dir)})
	if err != nil {
		return api.DocResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// ReplaceTabs makes tabs the tabs tide has open in the project ref.
func (c *Client) ReplaceTabs(ctx context.Context, ref string, tabs []api.Tab) (api.TabsResult, error) {
	if tabs == nil {
		tabs = []api.Tab{}
	}
	r, err := c.api.ReplaceTabsWithResponse(ctx, ref, api.TabsUpdate{Tabs: tabs})
	if err != nil {
		return api.TabsResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// TideView reads what tide shows as a whole, {} when none was saved.
func (c *Client) TideView(ctx context.Context) (json.RawMessage, error) {
	r, err := c.api.GetTideViewWithResponse(ctx)
	if err != nil {
		return nil, err
	}
	v, err := result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v.View)
}

// ReplaceTideView keeps view, a JSON object, as what tide shows as a whole.
func (c *Client) ReplaceTideView(ctx context.Context, view json.RawMessage) error {
	v, err := viewObject(view)
	if err != nil {
		return err
	}
	r, err := c.api.ReplaceTideViewWithResponse(ctx, api.View{View: v})
	if err != nil {
		return err
	}
	_, err = result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
	return err
}

// ProjectView reads what tide shows of the project ref, {} when none was saved.
func (c *Client) ProjectView(ctx context.Context, ref string) (json.RawMessage, error) {
	r, err := c.api.GetProjectViewWithResponse(ctx, ref)
	if err != nil {
		return nil, err
	}
	v, err := result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v.View)
}

// ReplaceProjectView keeps view, a JSON object, as what tide shows of the project ref.
func (c *Client) ReplaceProjectView(ctx context.Context, ref string, view json.RawMessage) error {
	v, err := viewObject(view)
	if err != nil {
		return err
	}
	r, err := c.api.ReplaceProjectViewWithResponse(ctx, ref, api.View{View: v})
	if err != nil {
		return err
	}
	_, err = result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
	return err
}

// viewObject is a view as the API's object.
func viewObject(view json.RawMessage) (map[string]any, error) {
	v := map[string]any{}
	return v, json.Unmarshal(view, &v)
}

// SyncDocsToGit writes the docs of the project named by project or holding dir into
// path, a directory relative to its main checkout.
func (c *Client) SyncDocsToGit(ctx context.Context, project, dir, path string) (api.DocSyncResult, error) {
	r, err := c.api.SyncDocsToGitWithResponse(ctx, api.SyncDocs{Project: nonEmpty(project), Dir: nonEmpty(dir), Path: path})
	if err != nil {
		return api.DocSyncResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// ListPlans returns the plans of the project named by project or holding dir, the
// archived too when archived.
func (c *Client) ListPlans(ctx context.Context, project, dir string, archived bool) (api.PlanList, error) {
	r, err := c.api.ListPlansWithResponse(ctx, &api.ListPlansParams{Project: nonEmpty(project), Dir: nonEmpty(dir), Archived: &archived})
	if err != nil {
		return api.PlanList{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// CreatePlan creates the plan body describes.
func (c *Client) CreatePlan(ctx context.Context, body api.CreatePlan) (api.PlanResult, error) {
	r, err := c.api.CreatePlanWithResponse(ctx, body)
	if err != nil {
		return api.PlanResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON201)
}

// GetPlan returns the plan ref names, "id:" and its id or its name in the project
// named by project or holding dir, with revision's content and definition, the
// latest when it is 0.
func (c *Client) GetPlan(ctx context.Context, ref, project, dir string, revision int64) (api.PlanContent, error) {
	params := api.GetPlanParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}
	if revision != 0 {
		params.Revision = &revision
	}
	r, err := c.api.GetPlanWithResponse(ctx, ref, &params)
	if err != nil {
		return api.PlanContent{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// RenamePlan gives the plan ref names, as GetPlan does, the name name.
func (c *Client) RenamePlan(ctx context.Context, ref, project, dir, name string) (api.PlanResult, error) {
	r, err := c.api.UpdatePlanWithResponse(ctx, ref, &api.UpdatePlanParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}, api.UpdatePlan{Name: &name})
	if err != nil {
		return api.PlanResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// UpdatePlanContent writes content as the next revision of the plan ref names, as
// GetPlan does, if base is still its latest.
func (c *Client) UpdatePlanContent(ctx context.Context, ref, project, dir string, base int64, content string) (api.PlanResult, error) {
	r, err := c.api.UpdatePlanContentWithResponse(ctx, ref, &api.UpdatePlanContentParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}, api.DocContentChange{Base: base, Content: content})
	if err != nil {
		return api.PlanResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// ListPlanRevisions returns the revisions of the plan ref names, as GetPlan does.
func (c *Client) ListPlanRevisions(ctx context.Context, ref, project, dir string) (api.DocRevisionList, error) {
	r, err := c.api.ListPlanRevisionsWithResponse(ctx, ref, &api.ListPlanRevisionsParams{Project: nonEmpty(project), Dir: nonEmpty(dir)})
	if err != nil {
		return api.DocRevisionList{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// DeletePlan removes the plan ref names, as GetPlan does.
func (c *Client) DeletePlan(ctx context.Context, ref, project, dir string) (api.PlanResult, error) {
	r, err := c.api.DeletePlanWithResponse(ctx, ref, &api.DeletePlanParams{Project: nonEmpty(project), Dir: nonEmpty(dir)})
	if err != nil {
		return api.PlanResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// RunPlan runs the plan ref names, as GetPlan does; parallel is the most tasks it
// runs at once, 0 for the setting's.
func (c *Client) RunPlan(ctx context.Context, ref, project, dir string, parallel int) (api.PlanResult, error) {
	var body api.RunPlan
	if parallel != 0 {
		body.Parallel = &parallel
	}
	r, err := c.api.RunPlanWithResponse(ctx, ref, &api.RunPlanParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}, body)
	if err != nil {
		return api.PlanResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// FinishPlanTask says the task of the plan ref names, as GetPlan does, is done,
// with summary.
func (c *Client) FinishPlanTask(ctx context.Context, ref, project, dir, task, summary string) (api.PlanTaskResult, error) {
	r, err := c.api.FinishPlanTaskWithResponse(ctx, ref, task, &api.FinishPlanTaskParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}, api.FinishPlanTask{Summary: summary})
	if err != nil {
		return api.PlanTaskResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// SetPlan changes the folder, the run's parallel or both of the plan ref names, as
// GetPlan does: body has no name, which RenamePlan sends.
func (c *Client) SetPlan(ctx context.Context, ref, project, dir string, body api.UpdatePlan) (api.PlanResult, error) {
	r, err := c.api.UpdatePlanWithResponse(ctx, ref, &api.UpdatePlanParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}, body)
	if err != nil {
		return api.PlanResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// PausePlan pauses the plan ref names, as GetPlan does.
func (c *Client) PausePlan(ctx context.Context, ref, project, dir string) (api.PlanResult, error) {
	r, err := c.api.PausePlanWithResponse(ctx, ref, &api.PausePlanParams{Project: nonEmpty(project), Dir: nonEmpty(dir)})
	if err != nil {
		return api.PlanResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// CopyPlan creates a draft named body's name from the latest revision of the plan
// ref names, as GetPlan does.
func (c *Client) CopyPlan(ctx context.Context, ref, project, dir string, body api.CopyPlan) (api.PlanResult, error) {
	r, err := c.api.CopyPlanWithResponse(ctx, ref, &api.CopyPlanParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}, body)
	if err != nil {
		return api.PlanResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON201)
}

// ArchivePlan archives the plan ref names, as GetPlan does.
func (c *Client) ArchivePlan(ctx context.Context, ref, project, dir string) (api.PlanResult, error) {
	r, err := c.api.ArchivePlanWithResponse(ctx, ref, &api.ArchivePlanParams{Project: nonEmpty(project), Dir: nonEmpty(dir)})
	if err != nil {
		return api.PlanResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// UnarchivePlan unarchives the plan ref names, as GetPlan does.
func (c *Client) UnarchivePlan(ctx context.Context, ref, project, dir string) (api.PlanResult, error) {
	r, err := c.api.UnarchivePlanWithResponse(ctx, ref, &api.UnarchivePlanParams{Project: nonEmpty(project), Dir: nonEmpty(dir)})
	if err != nil {
		return api.PlanResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// ResumePlan resumes the plan ref names, as GetPlan does.
func (c *Client) ResumePlan(ctx context.Context, ref, project, dir string) (api.PlanResult, error) {
	r, err := c.api.ResumePlanWithResponse(ctx, ref, &api.ResumePlanParams{Project: nonEmpty(project), Dir: nonEmpty(dir)})
	if err != nil {
		return api.PlanResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// CancelPlan cancels the plan ref names, as GetPlan does.
func (c *Client) CancelPlan(ctx context.Context, ref, project, dir string) (api.PlanResult, error) {
	r, err := c.api.CancelPlanWithResponse(ctx, ref, &api.CancelPlanParams{Project: nonEmpty(project), Dir: nonEmpty(dir)})
	if err != nil {
		return api.PlanResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// RetryPlanTask runs the task of the plan ref names, as GetPlan does, again, with a
// new agent when fresh.
func (c *Client) RetryPlanTask(ctx context.Context, ref, project, dir, task string, fresh bool) (api.PlanTaskResult, error) {
	r, err := c.api.RetryPlanTaskWithResponse(ctx, ref, task, &api.RetryPlanTaskParams{Project: nonEmpty(project), Dir: nonEmpty(dir)}, api.RetryPlanTask{Fresh: &fresh})
	if err != nil {
		return api.PlanTaskResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// LandPlanTask lands the task of the plan ref names, as GetPlan does, again.
func (c *Client) LandPlanTask(ctx context.Context, ref, project, dir, task string) (api.PlanTaskResult, error) {
	r, err := c.api.LandPlanTaskWithResponse(ctx, ref, task, &api.LandPlanTaskParams{Project: nonEmpty(project), Dir: nonEmpty(dir)})
	if err != nil {
		return api.PlanTaskResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// SkipPlanTask skips the task of the plan ref names, as GetPlan does.
func (c *Client) SkipPlanTask(ctx context.Context, ref, project, dir, task string) (api.PlanTaskResult, error) {
	r, err := c.api.SkipPlanTaskWithResponse(ctx, ref, task, &api.SkipPlanTaskParams{Project: nonEmpty(project), Dir: nonEmpty(dir)})
	if err != nil {
		return api.PlanTaskResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// ProjectSync is whether the project ref's docs go up to the hub, and how
// far they are.
func (c *Client) ProjectSync(ctx context.Context, ref string) (api.ProjectSyncEnvelope, error) {
	r, err := c.api.GetProjectSyncWithResponse(ctx, ref)
	if err != nil {
		return api.ProjectSyncEnvelope{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// SetProjectSync starts or stops pushing the project ref's docs to the hub.
func (c *Client) SetProjectSync(ctx context.Context, ref string, on bool) (api.ProjectSyncResult, error) {
	r, err := c.api.UpdateProjectSyncWithResponse(ctx, ref, api.ProjectSyncUpdate{On: on})
	if err != nil {
		return api.ProjectSyncResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}
