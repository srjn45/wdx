package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/warden/internal/api"
	"github.com/srjn45/warden/internal/client"
	"github.com/srjn45/warden/internal/daemon"
	"github.com/srjn45/warden/internal/identity"
	"github.com/srjn45/warden/internal/observe"
	"github.com/srjn45/warden/internal/style"
)

// result is what a command prints on success: the daemon's body as JSON, or
// text styled by st.
type result interface {
	text(w io.Writer, st style.Style)
}

// projectResult is a project a command changed.
type projectResult api.ProjectResult

func (r projectResult) text(w io.Writer, st style.Style) {
	p := r.Project
	switch {
	case r.Conflict == nil:
		fmt.Fprintf(w, "%s project %s\n", st.Success(string(r.Result)), projectName(st, p))
	case r.Result == api.ResultRegistered:
		fmt.Fprintf(w, "%s existing repository as project %s; %s\n", st.Success(string(r.Result)), projectName(st, p), st.Warning("nothing was created"))
	default:
		fmt.Fprintf(w, "%s existing project %s; %s\n", st.Success(string(r.Result)), projectName(st, p), st.Warning("nothing was created"))
	}
	fmt.Fprintf(w, "  %s %s\n", st.Muted("origin:   "), originText(st, p.Origin))
	for i, wt := range p.Worktrees {
		fmt.Fprintf(w, "  %s %s\n", st.Muted(fmt.Sprintf("%-10s", label(i, "worktrees:"))), wt.Path)
	}
	if r.Dropped != nil {
		for _, wt := range *r.Dropped {
			fmt.Fprintf(w, "  %s %s\n", st.Warning("dropped missing checkout"), wt.Path)
		}
	}
}

// projectDetail is a project with which of its checkouts are on disk.
type projectDetail api.ProjectEnvelope

func (d projectDetail) text(w io.Writer, st style.Style) {
	p := d.Project
	field := func(name, value string) {
		fmt.Fprintf(w, "  %s %s\n", st.Muted(fmt.Sprintf("%-10s", name)), value)
	}
	fmt.Fprintf(w, "project %s\n", projectName(st, p))
	field("state:", stateText(st, p.State))
	field("origin:", originText(st, p.Origin))
	field("created:", p.CreatedAt.Local().Format(time.DateTime))
	field("updated:", p.UpdatedAt.Local().Format(time.DateTime))
	if len(p.Worktrees) == 0 {
		field("worktrees:", st.Muted("(none)"))
	}
	for i, wt := range p.Worktrees {
		missing := ""
		if wt.Present != nil && !*wt.Present {
			missing = " " + st.Warning("(missing)")
		}
		field(label(i, "worktrees:"), wt.Path+missing)
	}
	if p.Orphaned != nil && *p.Orphaned {
		fmt.Fprintf(w, "  %s  no checkout on disk; %s\n", st.Warning("orphaned:"), hintText(st, missingCheckoutHint(p)))
	}
}

// projectList is the projects in state, every project when state is empty.
type projectList struct {
	api.ProjectList
	state api.ProjectState
}

func (l projectList) text(w io.Writer, st style.Style) {
	if len(l.Projects) == 0 {
		if l.state == "" {
			fmt.Fprintln(w, "no projects")
		} else {
			fmt.Fprintf(w, "no %s projects\n", l.state)
		}
		return
	}
	var table style.Table
	table.Row(st.Heading("NAME"), st.Heading("ID"), st.Heading("STATE"), st.Heading("ORIGIN"), st.Heading("WORKTREES"))
	for _, p := range l.Projects {
		origin := st.Muted("-")
		if p.Origin != nil {
			origin = *p.Origin
		}
		table.Row(st.Name(p.Name), st.Muted(p.Id), stateText(st, p.State), origin, strconv.Itoa(p.WorktreeCount))
	}
	table.Write(w)
}

type versionResponse struct {
	CLI    api.Version  `json:"cli"`
	Daemon *api.Version `json:"daemon"` // null when no daemon answers
}

func (r versionResponse) text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "%s %s %s\n", r.CLI.Name, st.Name(r.CLI.Version), st.Muted("("+r.CLI.Commit+")"))
	if r.Daemon == nil {
		fmt.Fprintf(w, "daemon %s\n", st.Warning("not running"))
		return
	}
	fmt.Fprintf(w, "daemon %s %s\n", st.Name(r.Daemon.Version), st.Muted("("+r.Daemon.Commit+")"))
}

// projectName is the name of p followed by its id.
func projectName(st style.Style, p api.Project) string {
	return st.Name(p.Name) + " " + st.Muted("("+p.Id+")")
}

func stateText(st style.Style, state api.ProjectState) string {
	if state == api.ProjectStateOpen {
		return st.Success(string(state))
	}
	return st.Muted(string(state))
}

func originText(st style.Style, origin *string) string {
	if origin == nil {
		return st.Muted("(none)")
	}
	return *origin
}

// label is name on the first line of a list and blank on the others.
func label(i int, name string) string {
	if i > 0 {
		return ""
	}
	return name
}

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
	} else if e, ok := errors.AsType[*daemon.AlreadyRunningError](err); ok {
		p = api.NewProblem(api.ErrorCodeAlreadyRunning, err.Error())
		if e.PID != 0 {
			p.Details = &map[string]any{"pid": e.PID}
		}
	} else if _, ok := errors.AsType[*observe.RuntimeMissingError](err); ok {
		p = api.NewProblem(api.ErrorCodeRuntimeMissing, err.Error())
		hint := "install Docker or Podman"
		p.Hint = &hint
	} else if e, ok := errors.AsType[*observe.PortInUseError](err); ok {
		p = api.NewProblem(api.ErrorCodePortInUse, err.Error())
		p.Details = &map[string]any{"port": e.Port}
		hint := "stop what listens on it"
		p.Hint = &hint
	} else if e, ok := errors.AsType[*observe.RuntimeError](err); ok {
		p = api.NewProblem(api.ErrorCodeRuntimeFailed, err.Error())
		p.Details = &map[string]any{"runtime": e.Runtime, "output": e.Output}
		hint := "check that " + e.Runtime + " ps works for your user"
		if e.Runtime == "docker" && strings.Contains(e.Output, "permission denied") {
			hint = "add your user to the docker group (sudo usermod -aG docker $USER) and log in again"
		}
		p.Hint = &hint
	} else if up, found := updateProblem(err); found {
		p = up
	} else if _, ok := errors.AsType[*client.UnavailableError](err); ok {
		p = api.NewProblem(api.ErrorCodeDaemonUnavailable, err.Error())
		hint := "run " + identity.Name + " daemon start"
		p.Hint = &hint
	} else {
		p = api.NewProblem(api.ErrorCodeInternal, err.Error())
	}
	if h, ok := errors.AsType[*hinted](err); ok {
		p.Hint = &h.hint
	}
	return p, api.Codes[p.Code].Exit
}

// problemCode returns the code of the problem the daemon returned, or "".
func problemCode(err error) api.ErrorCode {
	if e, ok := errors.AsType[*client.ProblemError](err); ok {
		return e.Problem.Code
	}
	return ""
}

// detailProject returns the project under key in the details of p.
func detailProject(p api.Problem, key string) (api.Project, bool) {
	if p.Details == nil {
		return api.Project{}, false
	}
	b, err := json.Marshal((*p.Details)[key])
	if err != nil {
		return api.Project{}, false
	}
	var project api.Project
	return project, json.Unmarshal(b, &project) == nil && project.Id != ""
}
