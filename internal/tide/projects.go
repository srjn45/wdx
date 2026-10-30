package tide

import (
	"encoding/json"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

// retryKey lists the projects again after the daemon could not.
const retryKey = "r"

// projectList is the projects tide knows, as last listed.
type projectList struct {
	projects []api.ProjectSummary // every project, by name
	loaded   bool                 // listed at least once
	listing  bool
	err      error // why the last list failed
}

// names returns the names of the projects in state, in order.
func (l *projectList) names(state api.ProjectState) []string {
	var names []string
	for _, p := range l.projects {
		if p.State == state {
			names = append(names, p.Name)
		}
	}
	return names
}

func (l *projectList) open() []string { return l.names(api.ProjectStateOpen) }

// projectsListed is the answer to listing the projects.
type projectsListed struct {
	projects []api.ProjectSummary
	err      error
}

// listProjects lists every project, in the order of their names.
func (m *Model) listProjects() tea.Cmd {
	m.projects.listing = true
	return func() tea.Msg {
		list, err := m.client.ListProjects(m.ctx, "")
		return projectsListed{projects: list.Projects, err: err}
	}
}

// listed takes the projects listed. The first list decides where tide
// starts: on the switcher when any project is open, on [Open Project]
// otherwise.
func (m *Model) listed(msg projectsListed) {
	l := m.projects
	first := !l.loaded
	l.listing, l.err = false, msg.err
	if msg.err != nil {
		m.log.Info("projects not listed", "err", msg.err)
		return
	}
	l.projects, l.loaded = msg.projects, true
	if !first {
		return
	}
	start := "open project"
	if len(l.open()) > 0 {
		m.switcher = newSwitcher(l, "")
		start = "switcher"
	}
	m.log.Info("tide starts", "on", start, "projects", len(l.projects), "open", len(l.open()))
}

// reopenProject asks tide to open the closed project name.
type reopenProject struct{ name string }

// createProject asks tide to send req, which opens a local repository,
// clones one or creates a project.
type createProject struct{ req api.CreateProject }

// recloneProject and removeProject answer a closed project's missing
// checkout: clone it again, or remove the project.
type (
	recloneProject struct{ name string }
	removeProject  struct{ name string }
)

// projectOpened is the answer to any of those: the project opened, or
// removed, or the error.
type projectOpened struct {
	name    string             // the project's, "" when a create failed
	removed bool               // the project was removed, as asked
	req     *api.CreateProject // the request, when it was a create
	err     error
}

func localRequest(path string) api.CreateProject {
	var source api.Source
	_ = source.FromLocalSource(api.LocalSource{Type: api.LocalSourceTypeLocal, Path: path})
	return api.CreateProject{Source: source}
}

func gitRequest(url string) api.CreateProject {
	var source api.Source
	_ = source.FromGitSource(api.GitSource{Type: api.GitSourceTypeGit, Url: url})
	return api.CreateProject{Source: source}
}

func newRequest(dir, name string) api.CreateProject {
	var source api.Source
	_ = source.FromNewSource(api.NewSource{Type: api.NewSourceTypeNew, Path: &dir})
	return api.CreateProject{Source: source, Name: &name}
}

// sourceType is req's source type: local, git or new.
func sourceType(req api.CreateProject) string {
	t, _ := req.Source.Discriminator()
	return t
}

// working is what a screen says while req runs.
func working(req api.CreateProject) string {
	switch sourceType(req) {
	case string(api.GitSourceTypeGit):
		return "cloning…"
	case string(api.NewSourceTypeNew):
		return "creating…"
	}
	return "opening…"
}

func (m *Model) reopen(name string) tea.Cmd {
	m.log.Info("project reopening", "name", name)
	return func() tea.Msg {
		_, err := m.client.UpdateProject(m.ctx, name, api.ProjectStateOpen, false)
		return projectOpened{name: name, err: err}
	}
}

func (m *Model) create(req api.CreateProject) tea.Cmd {
	attrs := []any{"source", sourceType(req)}
	switch source, _ := req.Source.ValueByDiscriminator(); source := source.(type) {
	case api.LocalSource:
		attrs = append(attrs, "path", source.Path)
	case api.GitSource:
		attrs = append(attrs, "url", source.Url)
	case api.NewSource:
		if source.Path != nil {
			attrs = append(attrs, "path", *source.Path)
		}
	}
	if req.Name != nil {
		attrs = append(attrs, "name", *req.Name)
	}
	if req.OnConflict != nil {
		attrs = append(attrs, "on_conflict", *req.OnConflict)
	}
	if req.OnMissingCheckout != nil {
		attrs = append(attrs, "on_missing_checkout", *req.OnMissingCheckout)
	}
	m.log.Info("project requested", attrs...)
	return func() tea.Msg {
		r, err := m.client.CreateProject(m.ctx, req)
		return projectOpened{name: r.Project.Name, removed: r.Result == api.ResultRemoved, req: &req, err: err}
	}
}

func (m *Model) reclone(name string) tea.Cmd {
	m.log.Info("project recloning", "name", name)
	return func() tea.Msg {
		_, err := m.client.RecloneProject(m.ctx, name)
		return projectOpened{name: name, err: err}
	}
}

func (m *Model) remove(name string) tea.Cmd {
	m.log.Info("project removing", "name", name)
	return func() tea.Msg {
		_, err := m.client.DeleteProject(m.ctx, name, false)
		return projectOpened{name: name, removed: err == nil, err: err}
	}
}

// opened takes the answer to opening a project. A failure goes to the
// screen that asked, or the switcher; the ones that need an answer push a
// screen asking for it, as the CLI prompts: a name taken, a create
// conflict, a missing checkout.
func (m *Model) opened(msg projectOpened) tea.Cmd {
	switch {
	case msg.err == nil && msg.removed:
		m.log.Info("project removed", "name", msg.name)
		m.toOpenProject("removed " + msg.name)
		return m.listProjects()
	case msg.err == nil:
		m.log.Info("project opened", "name", msg.name)
		return m.show(msg.name)
	}
	m.log.Info("project not opened", "name", msg.name, "err", msg.err)
	var cmd tea.Cmd
	if m.switcher != nil && m.switcher.opening == msg.name {
		cmd = m.switcher.update(msg)
	} else {
		cmd = m.top().update(msg)
	}
	var problem api.Problem
	if e, ok := errors.AsType[*client.ProblemError](msg.err); ok {
		problem = e.Problem
	}
	var asked screen
	switch req := msg.req; {
	case problem.Code == api.ErrorCodeCheckoutMissing:
		asked = m.missingCheckout(msg, problem)
	case req == nil:
	case sourceType(*req) == string(api.NewSourceTypeNew) && (problem.Code == api.ErrorCodeNameTaken || problem.Code == api.ErrorCodeRepoExists):
		asked = createConflict(*req, msg.err)
	case problem.Code == api.ErrorCodeNameTaken && req.Name == nil:
		asked = m.nameField(*req, msg.err)
	}
	if asked == nil {
		return cmd
	}
	if m.switcher != nil {
		m.switcher = nil
		m.focusProjectPane()
	}
	m.push(asked)
	return cmd
}

// nameField asks for another name for the project req would open, its own
// taken.
func (m *Model) nameField(req api.CreateProject, err error) screen {
	return &fieldScreen{
		heading: m.top().title(),
		prompt:  err.Error() + "\n\nAnother name for the project:",
		empty:   "type a name",
		working: working(req),
		submit: func(name string) tea.Msg {
			req.Name = &name
			return createProject{req: req}
		},
	}
}

// createConflict asks what to do when creating a project ran into one:
// its name is taken, or its directory is in a repository.
func createConflict(req api.CreateProject, err error) screen {
	open := req
	open.OnConflict = ptr(api.CreateProjectOnConflictOpen)
	return &choiceScreen{
		heading:  createTitle,
		question: err.Error(),
		choices: []choice{
			{label: "open that project", msg: createProject{req: open}, working: "opening…"},
			{label: "try another name", msg: popScreen{}},
			{label: "cancel", msg: cancelOpen{}},
		},
	}
}

// missingCheckout asks what to do with a known project none of whose
// checkouts is left: clone it again, when it has an origin, or remove it.
func (m *Model) missingCheckout(msg projectOpened, problem api.Problem) screen {
	var p api.Project
	var clonePath string
	if problem.Details != nil {
		if b, err := json.Marshal((*problem.Details)["project"]); err == nil {
			_ = json.Unmarshal(b, &p)
		}
		clonePath, _ = (*problem.Details)["clone_path"].(string)
	}
	reclone, remove := tea.Msg(recloneProject{name: p.Name}), tea.Msg(removeProject{name: p.Name})
	if req := msg.req; req != nil {
		again, gone := *req, *req
		again.OnMissingCheckout = ptr(api.CreateProjectOnMissingCheckoutReclone)
		gone.OnMissingCheckout = ptr(api.CreateProjectOnMissingCheckoutRemove)
		reclone, remove = createProject{req: again}, createProject{req: gone}
	}
	question := msg.err.Error()
	if clonePath != "" {
		question += " (was " + tildePath(clonePath, m.userHome) + ")"
	}
	var choices []choice
	if p.Origin != nil {
		choices = append(choices, choice{label: "clone it again", msg: reclone, working: "cloning…"})
	}
	choices = append(choices,
		choice{label: "remove it from " + identity.Name, msg: remove, working: "removing…"},
		choice{label: "cancel", msg: popScreen{}},
	)
	return &choiceScreen{heading: openProjectTitle + " › " + p.Name, question: question, choices: choices}
}

func ptr[T any](v T) *T { return &v }

// drawStatus draws why the projects are not known yet: they are being
// listed, or the daemon could not list them.
func (l *projectList) drawStatus(buf uv.Screen, area uv.Rectangle, st style.Style) {
	var lines []string
	_, unavailable := errors.AsType[*client.UnavailableError](l.err)
	switch {
	case l.listing || l.err == nil:
		lines = []string{st.Muted("loading…")}
	case unavailable:
		lines = []string{
			st.Failure("the daemon is not running"),
			"",
			"start it with " + st.Command(identity.Name+" daemon start"),
			"then press " + st.Command(retryKey) + " to try again",
		}
	default:
		lines = []string{
			st.Failure("the projects could not be listed:"),
			wrap(l.err.Error(), area.Dx()),
			"",
			"press " + st.Command(retryKey) + " to try again",
		}
	}
	uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, area)
}
