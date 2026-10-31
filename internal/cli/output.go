package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

// result is what a command prints on success: the daemon's body as JSON, or
// text styled by st.
type result interface {
	text(w io.Writer, st style.Style)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// label is name on the first line of a list and blank on the others.
func label(i int, name string) string {
	if i > 0 {
		return ""
	}
	return name
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
)

// What the daemon did with a setting set, besides resultNotRunning.
const (
	reloadApplied    = "applied"
	reloadNotApplied = "not_applied"
)

// printResult writes r to stdout as JSON or as text.
func printResult(cmd *cobra.Command, r result) error {
	invocationOf(cmd).response = r
	if jsonMode(cmd) {
		return writeJSON(cmd.OutOrStdout(), r)
	}
	r.text(cmd.OutOrStdout(), stdoutStyle(cmd))
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func jsonMode(cmd *cobra.Command) bool {
	on, _ := cmd.Flags().GetBool("json")
	return on
}

// usageError is a command line that cannot run as given.
type usageError struct {
	err error
}

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

// hinted adds a hint telling the user what to do about err.
type hinted struct {
	err  error
	hint string
}

func (e *hinted) Error() string { return e.err.Error() }
func (e *hinted) Unwrap() error { return e.err }

// problem returns err as the problem a command reports, and the exit code.
// The daemon's problems pass through; the CLI adds its hints.
func problem(err error) (api.Problem, int) {
	var p api.Problem
	if e, ok := errors.AsType[*client.ProblemError](err); ok {
		p = e.Problem
	} else if _, ok := errors.AsType[*usageError](err); ok {
		p = api.NewProblem(api.ErrorCodeUsage, err.Error())
	} else if dp, ok := daemonProblem(err); ok {
		p = dp
	} else if op, ok := observeProblem(err); ok {
		p = op
	} else if ap, ok := attachProblem(err); ok {
		p = ap
	} else if e, ok := errors.AsType[*config.Error](err); ok {
		p = settingsProblem(e)
	} else if ip, ok := depsProblem(err); ok {
		p = ip
	} else if up, ok := updateProblem(err); ok {
		p = up
	} else if unavailable(err) {
		p = api.NewProblem(api.ErrorCodeDaemonUnavailable, err.Error())
		hint := "run " + identity.Name + " daemon start"
		p.Hint = &hint
	} else {
		p = api.NewProblem(api.ErrorCodeInternal, err.Error())
	}
	if h, ok := errors.AsType[*hinted](err); ok {
		p.Hint = &h.hint
	}
	if dep, ok := missingDependency(p); ok && p.Hint == nil {
		hint := "run " + identity.Name + " deps install --name " + dep
		p.Hint = &hint
	}
	return p, api.Codes[p.Code].Exit
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

// fieldPrinter returns a func that writes an indented line of a name,
// padded to width, and its value.
func fieldPrinter(w io.Writer, st style.Style, width int) func(name, value string) {
	return func(name, value string) {
		fmt.Fprintf(w, "  %s %s\n", st.Muted(fmt.Sprintf("%-*s", width, name)), value)
	}
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
