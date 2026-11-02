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

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
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
// A method of "" is what the project says.
func (c *Client) LandWorktree(ctx context.Context, name, ref, dir, method string) (api.WorktreeLandResult, error) {
	params := &api.LandWorktreeParams{Project: nonEmpty(ref), Dir: nonEmpty(dir)}
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

// WorktreeFileContent returns what the file at path of the worktree name holds now.
func (c *Client) WorktreeFileContent(ctx context.Context, name, path, ref, dir string) (api.WorktreeFileContent, error) {
	r, err := c.api.GetWorktreeFileContentWithResponse(ctx, name, &api.GetWorktreeFileContentParams{Path: path, Project: nonEmpty(ref), Dir: nonEmpty(dir)})
	if err != nil {
		return api.WorktreeFileContent{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
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
