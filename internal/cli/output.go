package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/warden/internal/api"
	"github.com/srjn45/warden/internal/client"
	"github.com/srjn45/warden/internal/daemon"
	"github.com/srjn45/warden/internal/identity"
	"github.com/srjn45/warden/internal/observe"
)

// result is what a command prints on success: the daemon's body as JSON, or
// text.
type result interface {
	text(w io.Writer)
}

// projectResult is a project a command changed.
type projectResult api.ProjectResult

func (r projectResult) text(w io.Writer) {
	p := r.Project
	switch {
	case r.Conflict == nil:
		fmt.Fprintf(w, "%s project %s (%s)\n", r.Result, p.Name, p.Id)
	case r.Result == api.ResultRegistered:
		fmt.Fprintf(w, "registered existing repository as project %s (%s); nothing was created\n", p.Name, p.Id)
	default:
		fmt.Fprintf(w, "%s existing project %s (%s); nothing was created\n", r.Result, p.Name, p.Id)
	}
	fmt.Fprintf(w, "  origin:    %s\n", originText(p.Origin))
	for i, wt := range p.Worktrees {
		fmt.Fprintf(w, "  %-10s %s\n", label(i, "worktrees:"), wt.Path)
	}
	if r.Dropped != nil {
		for _, wt := range *r.Dropped {
			fmt.Fprintf(w, "  dropped missing checkout %s\n", wt.Path)
		}
	}
}

// projectDetail is a project with which of its checkouts are on disk.
type projectDetail api.ProjectEnvelope

func (d projectDetail) text(w io.Writer) {
	p := d.Project
	fmt.Fprintf(w, "project %s (%s)\n", p.Name, p.Id)
	fmt.Fprintf(w, "  state:     %s\n", p.State)
	fmt.Fprintf(w, "  origin:    %s\n", originText(p.Origin))
	fmt.Fprintf(w, "  created:   %s\n", p.CreatedAt.Local().Format(time.DateTime))
	fmt.Fprintf(w, "  updated:   %s\n", p.UpdatedAt.Local().Format(time.DateTime))
	if len(p.Worktrees) == 0 {
		fmt.Fprintf(w, "  worktrees: (none)\n")
	}
	for i, wt := range p.Worktrees {
		missing := ""
		if wt.Present != nil && !*wt.Present {
			missing = " (missing)"
		}
		fmt.Fprintf(w, "  %-10s %s%s\n", label(i, "worktrees:"), wt.Path, missing)
	}
	if p.Orphaned != nil && *p.Orphaned {
		fmt.Fprintf(w, "  orphaned:  no checkout on disk; %s\n", missingCheckoutHint(p))
	}
}

// projectList is the projects in state, every project when state is empty.
type projectList struct {
	api.ProjectList
	state api.ProjectState
}

func (l projectList) text(w io.Writer) {
	if len(l.Projects) == 0 {
		if l.state == "" {
			fmt.Fprintln(w, "no projects")
		} else {
			fmt.Fprintf(w, "no %s projects\n", l.state)
		}
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tID\tSTATE\tORIGIN\tWORKTREES")
	for _, p := range l.Projects {
		origin := "-"
		if p.Origin != nil {
			origin = *p.Origin
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n", p.Name, p.Id, p.State, origin, p.WorktreeCount)
	}
	tw.Flush()
}

type versionResponse struct {
	CLI    api.Version  `json:"cli"`
	Daemon *api.Version `json:"daemon"` // null when no daemon answers
}

func (r versionResponse) text(w io.Writer) {
	fmt.Fprintf(w, "%s %s (%s)\n", r.CLI.Name, r.CLI.Version, r.CLI.Commit)
	if r.Daemon == nil {
		fmt.Fprintln(w, "daemon not running")
		return
	}
	fmt.Fprintf(w, "daemon %s (%s)\n", r.Daemon.Version, r.Daemon.Commit)
}

func originText(origin *string) string {
	if origin == nil {
		return "(none)"
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
	r.text(cmd.OutOrStdout())
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
