package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/agent"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/doc"
	"github.com/srjn45/wdx/internal/gh"
	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/plan"
	"github.com/srjn45/wdx/internal/project"
	"github.com/srjn45/wdx/internal/terminal"
	"github.com/srjn45/wdx/internal/tidestate"
	"github.com/srjn45/wdx/internal/worktree"
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
	if e, ok := errors.AsType[*hubError](err); ok {
		return e.code, e.details
	}
	if e, ok := errors.AsType[*agent.UnavailableError](err); ok {
		return api.ErrorCodeAcaUnavailable, map[string]any{"aca": orNull(e.ACA), "reason": orNull(e.Reason), "program": orNull(e.Program)}
	}
	if _, ok := errors.AsType[*ReloadError](err); ok {
		return api.ErrorCodeReloadFailed, nil
	}
	if e, ok := errors.AsType[*UnknownSettingError](err); ok {
		return api.ErrorCodeNotFound, map[string]any{"setting": e.Key}
	}
	if e, ok := errors.AsType[*InvalidSettingError](err); ok {
		return api.ErrorCodeInvalidSetting, map[string]any{"setting": e.Key}
	}
	if _, ok := errors.AsType[*git.MissingError](err); ok {
		return api.ErrorCodeDependencyMissing, map[string]any{"dependency": "git"}
	}
	if e, ok := errors.AsType[*terminal.EditorMissingError](err); ok {
		return api.ErrorCodeDependencyMissing, map[string]any{"dependency": e.Editor}
	}
	if errors.Is(err, gh.ErrMissing) {
		return api.ErrorCodeDependencyMissing, map[string]any{"dependency": "gh"}
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
	if e, ok := errors.AsType[*worktree.InvalidPathError](err); ok {
		return api.ErrorCodeInvalidPath, map[string]any{"path": e.Path}
	}
	if e, ok := errors.AsType[*worktree.NotAFileError](err); ok {
		return api.ErrorCodeInvalidPath, map[string]any{"path": e.Path}
	}
	if e, ok := errors.AsType[*worktree.ExistsError](err); ok {
		return api.ErrorCodePathExists, map[string]any{"path": e.Path}
	}
	if e, ok := errors.AsType[*worktree.InvalidNameError](err); ok {
		return api.ErrorCodeInvalidName, map[string]any{"name": e.Name}
	}
	if e, ok := errors.AsType[*editorRunningError](err); ok {
		return api.ErrorCodeEditorRunning, map[string]any{"path": e.path, "editors": e.editors}
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
	if e, ok := errors.AsType[*plan.InvalidError](err); ok {
		return api.ErrorCodeInvalidPlan, map[string]any{"problems": problemViews(e.Problems)}
	}
	if e, ok := errors.AsType[*plan.NameTakenError](err); ok {
		return api.ErrorCodeNameTaken, map[string]any{"name": e.Name, "plan": planView(e.Plan)}
	}
	if e, ok := errors.AsType[*plan.ChangedError](err); ok {
		return api.ErrorCodePlanChanged, map[string]any{"plan": planView(e.Plan), "base": e.Base}
	}
	if e, ok := errors.AsType[*plan.InUseError](err); ok {
		return api.ErrorCodePlanInUse, map[string]any{"plan": planView(e.Plan), "parent": planView(e.Parent)}
	}
	if e, ok := errors.AsType[*plan.StartedError](err); ok {
		return api.ErrorCodePlanStarted, map[string]any{"plan": planView(e.Plan)}
	}
	if e, ok := errors.AsType[*plan.NotRunningError](err); ok {
		return api.ErrorCodePlanNotRunning, map[string]any{"plan": planView(e.Plan)}
	}
	if e, ok := errors.AsType[*plan.WaitsError](err); ok {
		code, plans := api.ErrorCodePlanNotLanded, append(e.NotLanded, e.NotPulled...)
		if len(e.NotDone) > 0 {
			code, plans = api.ErrorCodePlanNotFinished, e.NotDone
		}
		pulled := []string{}
		for _, p := range e.NotPulled {
			pulled = append(pulled, p.Name)
		}
		return code, map[string]any{"plan": planView(e.Plan), "plans": planViews(plans), "base": e.Base, "merged": pulled}
	}
	if e, ok := errors.AsType[*plan.NotFinishedError](err); ok {
		return api.ErrorCodePlanNotFinished, map[string]any{"plan": planView(e.Plan)}
	}
	if e, ok := errors.AsType[*plan.TaskStateError](err); ok {
		return api.ErrorCodeTaskState, map[string]any{"task": map[string]any{"id": e.Task.ID, "state": e.Task.State}}
	}
	if e, ok := errors.AsType[*plan.BranchExistsError](err); ok {
		return api.ErrorCodeBranchExists, map[string]any{"branch": e.Branch}
	}
	if e, ok := errors.AsType[*plan.WrongBranchError](err); ok {
		return api.ErrorCodeWrongBranch, map[string]any{"branch": e.Want}
	}
	if e, ok := errors.AsType[*doc.NameTakenError](err); ok {
		return api.ErrorCodeNameTaken, map[string]any{"name": e.Name, "doc": docView(e.Doc)}
	}
	if e, ok := errors.AsType[*doc.InvalidNameError](err); ok {
		return api.ErrorCodeInvalidName, map[string]any{"name": e.Name}
	}
	if e, ok := errors.AsType[*doc.InvalidError](err); ok {
		return api.ErrorCodeInvalidArgument, map[string]any{"argument": e.What}
	}
	if e, ok := errors.AsType[*tidestate.InvalidError](err); ok {
		return api.ErrorCodeInvalidArgument, map[string]any{"argument": "tabs", "index": e.Index}
	}
	if e, ok := errors.AsType[*doc.InvalidPathError](err); ok {
		return api.ErrorCodeInvalidPath, map[string]any{"path": e.Path}
	}
	if e, ok := errors.AsType[*doc.ChangedError](err); ok {
		return api.ErrorCodeDocChanged, map[string]any{"doc": docView(e.Doc), "base": e.Base}
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
	if e, ok := errors.AsType[*terminal.RelayError](err); ok {
		if errors.Is(e.Err, holder.ErrHolderVersionUnsupported) {
			return api.ErrorCodeHolderVersionUnsupported, map[string]any{"terminal": terminalView(e.Terminal)}
		}
		return api.ErrorCodeConnectionLost, map[string]any{"terminal": terminalView(e.Terminal)}
	}
	if e, ok := errors.AsType[*terminal.SendError](err); ok {
		return api.ErrorCodeSendFailed, map[string]any{"terminal": terminalView(e.Terminal)}
	}
	if e, ok := errors.AsType[*agent.RunningError](err); ok {
		return api.ErrorCodeAgentRunning, map[string]any{"agent": agentView(e.Agent)}
	}
	if e, ok := errors.AsType[*agent.BusyError](err); ok {
		return api.ErrorCodeAgentBusy, map[string]any{"agent": agentView(e.Agent)}
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
	if _, ok := errors.AsType[*worktree.NotLandedError](err); ok {
		return api.ErrorCodeWorktreeNotLanded, nil
	}
	if e, ok := errors.AsType[*git.RebaseError](err); ok {
		conflicts := e.Conflicts
		if conflicts == nil {
			conflicts = []string{}
		}
		return api.ErrorCodeRebaseFailed, map[string]any{"conflicts": conflicts}
	}
	if e, ok := errors.AsType[*worktree.NotCommittedError](err); ok {
		return api.ErrorCodeNotCommitted, map[string]any{"files": e.Files}
	}
	if _, ok := errors.AsType[*worktree.PushError](err); ok {
		return api.ErrorCodePushFailed, nil
	}
	if _, ok := errors.AsType[*git.WorktreeRemoveError](err); ok {
		return api.ErrorCodeWorktreeNotRemoved, nil
	}
	if e, ok := errors.AsType[*plan.BaseNotPulledError](err); ok {
		return api.ErrorCodeBaseNotPulled, map[string]any{"below": e.Below, "base": e.Default}
	}
	if e, ok := errors.AsType[*worktreeInPlanError](err); ok {
		return api.ErrorCodeWorktreeInPlan, map[string]any{"plan": planView(e.plan)}
	}
	if e, ok := errors.AsType[*projectClosedError](err); ok {
		return api.ErrorCodeProjectClosed, map[string]any{"project": projectView(e.project)}
	}
	if e, ok := errors.AsType[*projectInUseError](err); ok {
		details := map[string]any{"project": projectView(e.project), "terminals": terminalViews(e.terminals)}
		if len(e.agents) > 0 {
			details["agents"] = agentViews(e.agents)
		}
		if len(e.editors) > 0 {
			details["editors"] = editorViews(e.project, e.editors)
		}
		return api.ErrorCodeProjectInUse, details
	}
	switch {
	case errors.Is(err, project.ErrNotFound), errors.Is(err, terminal.ErrNotFound), errors.Is(err, agent.ErrNotFound), errors.Is(err, worktree.ErrNotFound), errors.Is(err, terminal.ErrMessageNotFound), errors.Is(err, doc.ErrNotFound), errors.Is(err, plan.ErrNotFound):
		return api.ErrorCodeNotFound, nil
	case errors.Is(err, doc.ErrNoRemote):
		return api.ErrorCodeNoRemote, nil
	case errors.Is(err, errLandNotConfigured):
		return api.ErrorCodeLandNotConfigured, nil
	case errors.Is(err, worktree.ErrAgentWorking):
		return api.ErrorCodeAgentWorking, nil
	case errors.Is(err, worktree.ErrNotCommitted):
		return api.ErrorCodeNotCommitted, nil
	case errors.Is(err, worktree.ErrNothingToLand):
		return api.ErrorCodeNothingToLand, nil
	case errors.Is(err, worktree.ErrNoLocalBase):
		return api.ErrorCodeNoLocalBase, nil
	case errors.Is(err, worktree.ErrDetachedHead):
		return api.ErrorCodeDetachedHead, nil
	case errors.Is(err, worktree.ErrLandingRunning):
		return api.ErrorCodeLandingRunning, nil
	case errors.Is(err, worktree.ErrWorktreeBusy):
		return api.ErrorCodeWorktreeBusy, nil
	case errors.Is(err, worktree.ErrIsClone):
		return api.ErrorCodeWorktreeIsClone, nil
	case errors.Is(err, project.ErrNotGitRepo):
		return api.ErrorCodeNotGitRepo, nil
	case errors.Is(err, project.ErrEmptyURL), errors.Is(err, project.ErrNoOrigin), errors.Is(err, plan.ErrParentParallel), errors.Is(err, plan.ErrAllDone), errors.Is(err, worktree.ErrEmptyComment), errors.Is(err, worktree.ErrNoComments):
		return api.ErrorCodeInvalidArgument, nil
	case errors.Is(err, aca.ErrNotSupported):
		return api.ErrorCodeAcaUnsupported, nil
	case errors.Is(err, agent.ErrUnknownACA):
		return api.ErrorCodeNotFound, map[string]any{"argument": "aca"}
	case errors.Is(err, agent.ErrInvalidModel):
		return api.ErrorCodeInvalidArgument, map[string]any{"argument": "model"}
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

// orNull is v, or nil for "", which a problem's details show as null.
func orNull(v string) any {
	if v == "" {
		return nil
	}
	return v
}
