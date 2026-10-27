package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/warden/internal/git"
	"github.com/srjn45/warden/internal/project"
)

// Error codes are a contract with scripts: never change or reuse one.
const (
	codeUsage           = "usage"
	codeInternal        = "internal"
	codeNotFound        = "not_found"
	codeNameTaken       = "name_taken"
	codeRepoExists      = "repo_exists"
	codeCheckoutMissing = "checkout_missing"
	codeNotGitRepo      = "not_git_repo"
	codeInvalidName     = "invalid_name"
	codeInvalidPath     = "invalid_path"
	codeInvalidArgument = "invalid_argument"
	codePathExists      = "path_exists"
	codeProjectMismatch = "project_mismatch"
	codeCloneFailed     = "clone_failed"
)

const (
	exitOK       = 0
	exitRejected = 1
	exitUsage    = 2
	exitSystem   = 3
)

// result is what a command prints on success.
type result interface {
	text(w io.Writer)
}

type response struct {
	OK       bool           `json:"ok"`
	Action   string         `json:"action"`
	Project  *projectView   `json:"project,omitempty"`
	Dropped  []worktreeView `json:"dropped,omitempty"`
	Conflict *errorView     `json:"conflict,omitempty"`
}

func projectResponse(action string, p project.Project) response {
	v := newProjectView(p)
	return response{OK: true, Action: action, Project: &v}
}

func openedResponse(o project.Opened) response {
	action := "opened"
	if o.Cloned {
		action = "cloned"
	} else if o.Registered {
		action = "registered"
	}
	r := projectResponse(action, o.Project)
	r.Dropped = worktreeViews(o.Dropped)
	return r
}

func (r response) text(w io.Writer) {
	p := r.Project
	switch {
	case r.Conflict == nil:
		fmt.Fprintf(w, "%s project %s (%s)\n", r.Action, p.Name, p.ID)
	case r.Action == "registered":
		fmt.Fprintf(w, "registered existing repository as project %s (%s); nothing was created\n", p.Name, p.ID)
	default:
		fmt.Fprintf(w, "%s existing project %s (%s); nothing was created\n", r.Action, p.Name, p.ID)
	}
	origin := "(none)"
	if p.Origin != nil {
		origin = *p.Origin
	}
	fmt.Fprintf(w, "  origin:    %s\n", origin)
	for i, wt := range p.Worktrees {
		label := "worktrees:"
		if i > 0 {
			label = ""
		}
		fmt.Fprintf(w, "  %-10s %s\n", label, wt.Path)
	}
	for _, wt := range r.Dropped {
		fmt.Fprintf(w, "  dropped missing checkout %s\n", wt.Path)
	}
}

type projectView struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Origin    *string        `json:"origin"`
	State     string         `json:"state"`
	Worktrees []worktreeView `json:"worktrees"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
}

func newProjectView(p project.Project) projectView {
	v := projectView{
		ID:        p.ID,
		Name:      p.Name,
		State:     string(p.State),
		Worktrees: worktreeViews(p.Worktrees),
		CreatedAt: timestamp(p.CreatedAt),
		UpdatedAt: timestamp(p.UpdatedAt),
	}
	if p.OriginURL != "" {
		v.Origin = &p.OriginURL
	}
	return v
}

type worktreeView struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

func worktreeViews(wts []project.Worktree) []worktreeView {
	views := make([]worktreeView, 0, len(wts))
	for _, w := range wts {
		views = append(views, worktreeView{ID: w.ID, Path: w.Path})
	}
	return views
}

func timestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

type versionResponse struct {
	OK      bool   `json:"ok"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

func (r versionResponse) text(w io.Writer) {
	fmt.Fprintf(w, "%s %s (%s)\n", r.Name, r.Version, r.Commit)
}

// printResult writes r to stdout as JSON or as text.
func printResult(cmd *cobra.Command, r result) error {
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

type errorResponse struct {
	OK    bool      `json:"ok"`
	Error errorView `json:"error"`
}

type errorView struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Hint    *string        `json:"hint"`
	Details map[string]any `json:"details"`
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

// classify maps err to its error code and details, and the exit code.
func classify(err error) (errorView, int) {
	v := errorView{Code: codeInternal, Message: err.Error()}
	if h, ok := errors.AsType[*hinted](err); ok {
		v.Hint = &h.hint
	}
	if _, ok := errors.AsType[*usageError](err); ok {
		v.Code = codeUsage
		return v, exitUsage
	}

	exit := exitRejected
	if e, ok := errors.AsType[*project.NameTakenError](err); ok {
		v.Code, v.Details = codeNameTaken, map[string]any{"name": e.Name, "project": newProjectView(e.Project)}
	} else if e, ok := errors.AsType[*project.RepoExistsError](err); ok {
		var p *projectView
		if e.Project != nil {
			view := newProjectView(*e.Project)
			p = &view
		}
		v.Code, v.Details = codeRepoExists, map[string]any{"path": e.Dest, "root": e.Root, "project": p}
	} else if e, ok := errors.AsType[*project.CheckoutMissingError](err); ok {
		v.Code, v.Details = codeCheckoutMissing, map[string]any{"project": newProjectView(e.Project)}
	} else if e, ok := errors.AsType[*project.InvalidNameError](err); ok {
		v.Code, v.Details = codeInvalidName, map[string]any{"name": e.Name}
	} else if e, ok := errors.AsType[*project.InvalidPathError](err); ok {
		v.Code, v.Details = codeInvalidPath, map[string]any{"path": e.Path}
	} else if e, ok := errors.AsType[*project.PathExistsError](err); ok {
		v.Code, v.Details = codePathExists, map[string]any{"path": e.Path}
	} else if e, ok := errors.AsType[*project.ProjectMismatchError](err); ok {
		v.Code, v.Details = codeProjectMismatch, map[string]any{
			"path":        e.Root,
			"origin":      e.Origin,
			"worktree_of": newProjectView(e.ByClone),
			"origin_of":   newProjectView(e.ByOrigin),
		}
	} else if e, ok := errors.AsType[*git.CloneError](err); ok {
		v.Code, v.Details = codeCloneFailed, map[string]any{"url": e.URL, "output": e.Output}
	} else if errors.Is(err, project.ErrNotFound) {
		v.Code = codeNotFound
	} else if errors.Is(err, project.ErrNotGitRepo) {
		v.Code = codeNotGitRepo
	} else if errors.Is(err, project.ErrEmptyURL) {
		v.Code = codeInvalidArgument
	} else {
		exit = exitSystem
	}
	return v, exit
}
