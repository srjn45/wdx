package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/srjn45/wdx/internal/agent"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/project"
	"github.com/srjn45/wdx/internal/terminal"
)

// requestError is a request that cannot be served as sent.
type requestError struct {
	err error
}

func (e *requestError) Error() string { return e.err.Error() }
func (e *requestError) Unwrap() error { return e.err }

// problemOf returns err as the problem the API reports.
func problemOf(ctx context.Context, err error) api.Problem {
	code, details := classify(err)
	p := api.NewProblem(code, err.Error())
	if details != nil {
		p.Details = &details
	}
	p.DebugId = debugID(ctx)
	return p
}

// classify maps err to its error code and the values it involves.
func classify(err error) (api.ErrorCode, map[string]any) {
	if _, ok := errors.AsType[*requestError](err); ok {
		return api.ErrorCodeUsage, nil
	}
	if _, ok := errors.AsType[*ReloadError](err); ok {
		return api.ErrorCodeReloadFailed, nil
	}
	if _, ok := errors.AsType[*git.MissingError](err); ok {
		return api.ErrorCodeDependencyMissing, map[string]any{"dependency": "git"}
	}
	if e, ok := errors.AsType[*project.NameTakenError](err); ok {
		return api.ErrorCodeNameTaken, map[string]any{"name": e.Name, "project": projectView(e.Project)}
	}
	if e, ok := errors.AsType[*project.RepoExistsError](err); ok {
		var p *api.Project
		if e.Project != nil {
			v := projectView(*e.Project)
			p = &v
		}
		return api.ErrorCodeRepoExists, map[string]any{"path": e.Dest, "root": e.Root, "project": p}
	}
	if e, ok := errors.AsType[*project.CheckoutMissingError](err); ok {
		return api.ErrorCodeCheckoutMissing, map[string]any{"project": projectView(e.Project), "clone_path": e.Project.ClonePath()}
	}
	if e, ok := errors.AsType[*project.InvalidNameError](err); ok {
		return api.ErrorCodeInvalidName, map[string]any{"name": e.Name}
	}
	if e, ok := errors.AsType[*project.InvalidPathError](err); ok {
		return api.ErrorCodeInvalidPath, map[string]any{"path": e.Path}
	}
	if e, ok := errors.AsType[*project.InvalidWorktreeNameError](err); ok {
		return api.ErrorCodeInvalidName, map[string]any{"name": e.Name}
	}
	if e, ok := errors.AsType[*project.BranchExistsError](err); ok {
		return api.ErrorCodeBranchExists, map[string]any{"branch": e.Branch}
	}
	if e, ok := errors.AsType[*git.WorktreeError](err); ok {
		return api.ErrorCodeWorktreeFailed, map[string]any{"path": e.Path, "output": e.Output}
	}
	if e, ok := errors.AsType[*project.PathExistsError](err); ok {
		return api.ErrorCodePathExists, map[string]any{"path": e.Path}
	}
	if e, ok := errors.AsType[*project.ProjectMismatchError](err); ok {
		return api.ErrorCodeProjectMismatch, map[string]any{
			"path":        e.Root,
			"origin":      e.Origin,
			"worktree_of": projectView(e.ByClone),
			"origin_of":   projectView(e.ByOrigin),
		}
	}
	if e, ok := errors.AsType[*git.CloneError](err); ok {
		return api.ErrorCodeCloneFailed, map[string]any{"url": e.URL, "output": e.Output}
	}
	if e, ok := errors.AsType[*terminal.NameTakenError](err); ok {
		// An agent's terminal row is never shown: the message names it.
		if e.Terminal.Kind != terminal.KindShell {
			return api.ErrorCodeNameTaken, map[string]any{"name": e.Name}
		}
		return api.ErrorCodeNameTaken, map[string]any{"name": e.Name, "terminal": terminalView(e.Terminal)}
	}
	if e, ok := errors.AsType[*terminal.InvalidNameError](err); ok {
		return api.ErrorCodeInvalidName, map[string]any{"name": e.Name}
	}
	if e, ok := errors.AsType[*terminal.RunningError](err); ok {
		return api.ErrorCodeTerminalRunning, map[string]any{"terminal": terminalView(e.Terminal)}
	}
	if e, ok := errors.AsType[*terminal.StartError](err); ok {
		return api.ErrorCodeStartFailed, map[string]any{"terminal": terminalView(e.Terminal)}
	}
	if e, ok := errors.AsType[*terminal.NotRunningError](err); ok {
		return api.ErrorCodeTerminalNotRunning, map[string]any{"terminal": terminalView(e.Terminal)}
	}
	if e, ok := errors.AsType[*terminal.SendError](err); ok {
		return api.ErrorCodeSendFailed, map[string]any{"terminal": terminalView(e.Terminal)}
	}
	if e, ok := errors.AsType[*agent.RunningError](err); ok {
		return api.ErrorCodeAgentRunning, map[string]any{"agent": agentView(e.Agent)}
	}
	if e, ok := errors.AsType[*agent.RemovedError](err); ok {
		return api.ErrorCodeAgentRemoved, map[string]any{"agent": agentView(e.Agent)}
	}
	if e, ok := errors.AsType[*agent.NotRunningError](err); ok {
		return api.ErrorCodeAgentNotRunning, map[string]any{"agent": agentView(e.Agent)}
	}
	if e, ok := errors.AsType[*agent.SendError](err); ok {
		return api.ErrorCodeSendFailed, map[string]any{"agent": agentView(e.Agent)}
	}
	if e, ok := errors.AsType[*agent.StartError](err); ok {
		return api.ErrorCodeStartFailed, map[string]any{"agent": agentView(e.Agent)}
	}
	if e, ok := errors.AsType[*projectClosedError](err); ok {
		return api.ErrorCodeProjectClosed, map[string]any{"project": projectView(e.project)}
	}
	if e, ok := errors.AsType[*projectInUseError](err); ok {
		details := map[string]any{"project": projectView(e.project), "terminals": terminalViews(e.terminals)}
		if len(e.agents) > 0 {
			details["agents"] = agentViews(e.agents)
		}
		return api.ErrorCodeProjectInUse, details
	}
	switch {
	case errors.Is(err, project.ErrNotFound), errors.Is(err, terminal.ErrNotFound), errors.Is(err, agent.ErrNotFound):
		return api.ErrorCodeNotFound, nil
	case errors.Is(err, project.ErrNotGitRepo):
		return api.ErrorCodeNotGitRepo, nil
	case errors.Is(err, project.ErrEmptyURL), errors.Is(err, project.ErrNoOrigin):
		return api.ErrorCodeInvalidArgument, nil
	}
	return api.ErrorCodeInternal, nil
}

func writeProblem(w http.ResponseWriter, p api.Problem) {
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
