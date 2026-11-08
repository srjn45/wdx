package cli

import (
	"encoding/json"
	"errors"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/settings"
)

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// The results of the commands that do not go through the API, whose
// results are api.Result.
const (
	resultRunning    = "running"
	resultStarted    = "started"
	resultStopped    = "stopped"
	resultNotRunning = "not_running"
	resultRestarted  = "restarted"
	resultUpdated    = "updated"
	resultUpToDate   = "up_to_date"
	resultRolledBack = "rolled_back"
	resultWritten    = "written"
	resultUnchanged  = "unchanged"
	resultInstalled  = "installed"
	resultReady      = "ready"
	resultSigned     = "signed"
)

// What the daemon did with a setting set, besides resultNotRunning.
const (
	reloadApplied    = "applied"
	reloadNotApplied = "not_applied"
)

// problem returns err as the problem wdx reports, when it is one of wdx's
// own errors: the daemon's problems pass through, and the CLI adds its hints.
// A settings error is left to the framework, in the order wdx has always
// mapped them.
func problem(err error) (clikit.Problem, bool) {
	p, ok := apiProblem(err)
	if !ok {
		return clikit.Problem{}, false
	}
	if dep, ok := missingDependency(p); ok && p.Hint == nil {
		hint := "run " + identity.Name + " deps install --name " + dep
		p.Hint = &hint
	}
	q := toProblem(p)
	q.Lines = problemLines(p, err)
	return q, true
}

func apiProblem(err error) (api.Problem, bool) {
	if e, ok := errors.AsType[*client.ProblemError](err); ok {
		return e.Problem, true
	}
	for _, mapper := range []func(error) (api.Problem, bool){daemonProblem, tideProblem, observeProblem, attachProblem} {
		if p, ok := mapper(err); ok {
			return p, true
		}
	}
	if _, ok := errors.AsType[*settings.Error](err); ok {
		return api.Problem{}, false
	}
	for _, mapper := range []func(error) (api.Problem, bool){depsProblem, updateProblem} {
		if p, ok := mapper(err); ok {
			return p, true
		}
	}
	if unavailable(err) {
		p := api.NewProblem(api.ErrorCodeDaemonUnavailable, err.Error())
		hint := "run " + identity.Name + " daemon start"
		p.Hint = &hint
		return p, true
	}
	return api.Problem{}, false
}

// toProblem is p as the framework reports it.
func toProblem(p api.Problem) clikit.Problem {
	return clikit.Problem{
		Code: string(p.Code), DebugId: p.DebugId, Detail: p.Detail, Details: p.Details,
		Hint: p.Hint, Status: p.Status, Title: p.Title, Type: p.Type,
	}
}

// missingDependency returns the dependency a dependency_missing problem
// names.
func missingDependency(p api.Problem) (string, bool) {
	if p.Code != api.ErrorCodeDependencyMissing || p.Details == nil {
		return "", false
	}
	dep, ok := (*p.Details)["dependency"].(string)
	return dep, ok && dep != ""
}

// problemCode returns the code of the problem the daemon returned, or "".
func problemCode(err error) api.ErrorCode {
	if e, ok := errors.AsType[*client.ProblemError](err); ok {
		return e.Problem.Code
	}
	return ""
}

// decodeDetail decodes the value under key in the details of p into v.
func decodeDetail(p api.Problem, key string, v any) bool {
	if p.Details == nil {
		return false
	}
	raw, ok := (*p.Details)[key]
	if !ok {
		return false
	}
	b, err := json.Marshal(raw)
	return err == nil && json.Unmarshal(b, v) == nil
}

// detailProject returns the project under key in the details of p.
func detailProject(p api.Problem, key string) (api.Project, bool) {
	var project api.Project
	return project, decodeDetail(p, key, &project) && project.Id != ""
}

// unavailable reports whether err says the daemon did not answer.
func unavailable(err error) bool {
	_, ok := errors.AsType[*client.UnavailableError](err)
	return ok
}

func ptr[T any](v T) *T {
	return &v
}

// optional is nil for the zero value, a pointer to v otherwise.
func optional[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}
