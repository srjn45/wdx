package tide

import (
	"errors"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
)

// agentForm is the box of C-q a and C-q c, asking what a new agent is created with
// (the wdx doc AGENT_CREATE, step 3). An empty field is left to the daemon.
type agentForm struct {
	parent      *api.Agent // the agent C-q c creates it under; nil for C-q a
	dir         string     // where it starts when no worktree is typed
	in          string     // the worktree that is, as the field's hint names it
	field       formField  // the one with the cursor
	name        string
	model       string // what is typed in the model field, which filters the list
	worktree    string
	permissions api.AgentPermissions
	prompt      string

	models     []api.AcaModel // the ACA's, once listed
	modelsRead bool
	modelsErr  string
	selected   int // the match chosen, an index into matches()
	top        int // the first match shown

	busy bool   // the request is on its way
	err  string // why the last one failed
}

type formField int

const (
	formName formField = iota
	formModel
	formWorktree
	formPermissions
	formPrompt
	formFields // how many there are
)

// modelRows is how many rows the model list always takes: Cursor offers hundreds.
const modelRows = 4

// formLabelWidth is the width of the labels, the longest's and a gap.
const formLabelWidth = len("Permissions") + 2

// formFieldWidth is how much of a value shows; a longer one loses its start.
const formFieldWidth = 40

// defaultModel is the list's row for the ACA's own default, which passes no model.
var defaultModel = api.AcaModel{Id: "", Name: "Default"}

// modelsListed is the answer to listing the ACA's models.
type modelsListed struct {
	models []api.AcaModel
	err    error
}

// openAgentForm opens the box for a new agent under parent, or at the top level
// when it is nil, and asks for the models.
func (m *Model) openAgentForm(parent *api.Agent) tea.Cmd {
	m.switcher, m.opening = nil, nil
	f := &agentForm{parent: parent, permissions: api.AgentPermissionsFull}
	if parent != nil {
		f.dir, f.in = parent.Dir, "the parent's"
		if parent.Worktree != nil {
			f.in += ", " + *parent.Worktree
		}
	} else {
		f.dir, _ = m.checkoutDir()
		f.in = m.worktreeAt(f.dir)
	}
	m.agentForm = f
	m.log.Info("agent form opened", "parent", parentName(parent), "dir", f.dir)
	if m.aca == "" {
		f.modelsRead = true
		return nil
	}
	aca := m.aca
	return func() tea.Msg {
		r, err := m.client.ListAcaModels(m.ctx, aca)
		return modelsListed{models: r.Models, err: err}
	}
}

func parentName(a *api.Agent) string {
	if a == nil {
		return ""
	}
	return a.Name
}

// worktreeAt names the project's worktree at dir, as the stream does.
func (m *Model) worktreeAt(dir string) string {
	for _, w := range m.worktreeStatuses() {
		if w.Path == dir {
			return w.Name
		}
	}
	return filepath.Base(dir)
}

func (m *Model) modelsWereListed(msg modelsListed) {
	f := m.agentForm
	if f == nil {
		return
	}
	f.modelsRead = true
	if msg.err != nil {
		m.log.Info("models not listed", "err", msg.err)
		f.modelsErr = msg.err.Error()
		return
	}
	m.log.Info("models listed", "count", len(msg.models))
	f.models = msg.models
}

// matches are the models what is typed matches, by words on id and name, then
// the ACA's default, which matches when nothing is typed.
func (f *agentForm) matches() []api.AcaModel {
	words := strings.Fields(strings.ToLower(f.model))
	var list []api.AcaModel
	for _, model := range f.models {
		if matches(model.Id+" "+model.Name, words) {
			list = append(list, model)
		}
	}
	if matches("default its own setting", words) {
		list = append(list, defaultModel)
	}
	return list
}

// chosenModel is what the request passes: the match selected, or what is typed
// when nothing matches, so that a model not in the list goes through.
func (f *agentForm) chosenModel() string {
	if list := f.matches(); len(list) > 0 {
		return list[min(f.selected, len(list)-1)].Id
	}
	return strings.TrimSpace(f.model)
}

// moveSelection moves the model chosen by delta, scrolling the list with it.
func (f *agentForm) moveSelection(delta int) {
	n := len(f.matches())
	if n == 0 {
		return
	}
	f.selected = max(0, min(n-1, f.selected+delta))
	if f.selected < f.top {
		f.top = f.selected
	}
	if f.selected >= f.top+modelRows {
		f.top = f.selected - modelRows + 1
	}
}

// value is the text of the field with the cursor, for typing into.
func (f *agentForm) value() *string {
	switch f.field {
	case formName:
		return &f.name
	case formModel:
		return &f.model
	case formWorktree:
		return &f.worktree
	case formPrompt:
		return &f.prompt
	}
	return nil
}

// add types text into the field with the cursor; what filters the models starts the
// list again at its top.
func (f *agentForm) add(text string) {
	if v := f.value(); v != nil {
		*v += text
		if f.field == formModel {
			f.selected, f.top = 0, 0
		}
	}
}

// agentFormKey takes a key while the box is open. While the request is on its
// way only Esc counts, which closes the box: the daemon finishes the request.
func (m *Model) agentFormKey(msg tea.KeyPressMsg) tea.Cmd {
	f := m.agentForm
	name := msg.String()
	if name == "esc" {
		m.agentForm = nil
		m.log.Info("agent form closed", "busy", f.busy)
		return nil
	}
	if f.busy {
		return nil
	}
	switch {
	case name == "enter":
		return m.submitAgentForm()
	case name == "tab":
		f.field = (f.field + 1) % formFields
	case name == "shift+tab":
		f.field = (f.field + formFields - 1) % formFields
	case f.field == formModel && name == "up":
		f.moveSelection(-1)
	case f.field == formModel && name == "down":
		f.moveSelection(1)
	case f.field == formPermissions && (name == "left" || name == "right" || name == "space"):
		if f.permissions == api.AgentPermissionsFull {
			f.permissions = api.AgentPermissionsAsk
		} else {
			f.permissions = api.AgentPermissionsFull
		}
	case name == "backspace":
		if v := f.value(); v != nil && *v != "" {
			r := []rune(*v)
			*v = string(r[:len(r)-1])
			if f.field == formModel {
				f.selected, f.top = 0, 0
			}
		}
	case typed(msg):
		f.add(msg.Text)
	}
	return nil
}

// pasteIntoAgentForm types a paste into the field with the cursor, its line breaks
// made spaces: every field is one line.
func (m *Model) pasteIntoAgentForm(text string) {
	if f := m.agentForm; !f.busy {
		f.add(strings.Join(strings.Fields(text), " "))
	}
}

// submitAgentForm sends what the box asks for, leaving out what is empty.
func (m *Model) submitAgentForm() tea.Cmd {
	f := m.agentForm
	f.busy, f.err = true, ""
	body := api.CreateAgent{Dir: f.dir, Permissions: &f.permissions}
	if f.parent != nil {
		ref := "id:" + f.parent.Id
		body.Parent = &ref
	}
	if name := strings.TrimSpace(f.name); name != "" {
		body.Name = &name
	}
	if model := f.chosenModel(); model != "" {
		body.Model = &model
	}
	if worktree := strings.TrimSpace(f.worktree); worktree != "" {
		body.Worktree = &worktree
	}
	if prompt := strings.TrimSpace(f.prompt); prompt != "" {
		body.Prompt = &prompt
	}
	return m.sendCreateAgent(body)
}

// agentFormFailed keeps the box open with why, its cursor on the field the error
// is about, for another try.
func (m *Model) agentFormFailed(err error) {
	f := m.agentForm
	f.busy, f.err = false, err.Error()
	if e, ok := errors.AsType[*client.ProblemError](err); ok {
		switch e.Problem.Code {
		case api.ErrorCodeNameTaken, api.ErrorCodeInvalidName:
			f.field = formName
		case api.ErrorCodeInvalidArgument:
			f.field = formModel
		case api.ErrorCodeBranchExists, api.ErrorCodePathExists, api.ErrorCodeWorktreeFailed:
			f.field = formWorktree
		}
	}
}

// agentFormPopup is what the box shows.
func (m *Model) agentFormPopup() popup {
	f, st := m.agentForm, m.st
	title := "New agent in " + m.following.project.Name
	if f.parent != nil {
		title = "New child of " + f.parent.Name
	}
	var body []string
	row := func(field formField, label, value string) {
		mark := "  "
		if field == f.field {
			mark, label = "▸ ", st.Name(label)
		}
		body = append(body, mark+label+strings.Repeat(" ", formLabelWidth-ansi.StringWidth(label))+value)
	}
	under := func(text string) {
		body = append(body, strings.Repeat(" ", 2+formLabelWidth)+text)
	}
	text := func(field formField, v string) string {
		if field != f.field {
			return v
		}
		v += "_"
		if over := ansi.StringWidth(v) - formFieldWidth; over > 0 {
			v = "…" + ansi.TruncateLeft(v, over+1, "")
		}
		return v
	}
	row(formName, "Name", text(formName, f.name))
	under(st.Muted("empty: a generated one"))
	row(formModel, "Model", text(formModel, f.model))
	body = append(body, m.modelRows()...)
	row(formWorktree, "Worktree", text(formWorktree, f.worktree))
	under(st.Muted("empty: " + f.in + "; a new name creates it"))
	row(formPermissions, "Permissions", st.Muted("‹ ")+string(f.permissions)+st.Muted(" ›"))
	if f.permissions == api.AgentPermissionsFull {
		under(st.Muted("no approvals, network access, no sandbox"))
	} else {
		under(st.Muted("as your settings for it say"))
	}
	row(formPrompt, "Prompt", text(formPrompt, f.prompt))
	under(st.Muted("empty: none"))
	switch {
	case f.busy:
		body = append(body, "", st.Muted("creating…"))
	case f.err != "":
		body = append(body, "", st.Failure(f.err))
	}
	footer := st.Command("Enter") + " create  " + st.Command("Tab") + " next  " + st.Command("Esc") + " cancel"
	switch f.field {
	case formModel:
		footer = st.Command("Enter") + " create  " + st.Command("Tab") + " next  " + st.Command("↑↓") + " model  " + st.Command("Esc") + " cancel"
	case formPermissions:
		footer = st.Command("Enter") + " create  " + st.Command("Tab") + " next  " + st.Command("←→") + " change  " + st.Command("Esc") + " cancel"
	}
	return popup{title: title, body: body, footer: footer}
}

// modelRows are the model list's rows, always modelRows of them.
func (m *Model) modelRows() []string {
	f, st := m.agentForm, m.st
	indent := strings.Repeat(" ", 2+formLabelWidth)
	var rows []string
	switch list := f.matches(); {
	case !f.modelsRead:
		rows = append(rows, indent+st.Muted("loading models…"))
	default:
		if f.modelsErr != "" {
			rows = append(rows, indent+st.Failure("models not listed: "+f.modelsErr))
		}
		if len(list) == 0 {
			rows = append(rows, indent+st.Muted("none matches: what is typed is used"))
		}
		idWidth := 0
		for _, model := range list {
			idWidth = max(idWidth, ansi.StringWidth(modelLabel(model)))
		}
		for i := f.top; i < len(list) && len(rows) < modelRows; i++ {
			model := list[i]
			label := modelLabel(model)
			line := label + strings.Repeat(" ", idWidth-ansi.StringWidth(label)) + "  " + st.Muted(modelNote(model))
			if i == f.selected {
				rows = append(rows, indent[2:]+"▸ "+st.Name(line))
			} else {
				rows = append(rows, indent+line)
			}
		}
	}
	for len(rows) < modelRows {
		rows = append(rows, "")
	}
	return rows
}

// modelLabel is how the list names a model: its id, or Default for the ACA's own.
func modelLabel(model api.AcaModel) string {
	if model.Id == "" {
		return model.Name
	}
	return model.Id
}

// modelNote is what the list says after a model's label.
func modelNote(model api.AcaModel) string {
	switch {
	case model.Id == "":
		return "its own setting"
	case model.Default:
		return model.Name + " (default)"
	}
	return model.Name
}
