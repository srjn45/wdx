package tide

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
)

// L on a worktree's commits lands them: wdx does the git steps, and an agent is only
// asked to rebase when wdx's own try stops on a conflict (docs/TIDE_WORKTREES.md,
// "Landing and removing").

const (
	landKey     = "L"
	commitFirst = "commit first"
)

// landBox says what is about to land, and how the project lands.
type landBox struct {
	worktree string
	method   api.ProjectLandingMethod
	check    string
	push     bool
	editing  bool // the settings are being typed: the first time, or after m
	busy     bool
	err      string
}

// landSettingsRead is the answer to asking how the project lands.
type landSettingsRead struct {
	worktree string
	settings api.ProjectLanding
	err      error
}

// landSettingsSaved is the answer to saving how the project lands.
type landSettingsSaved struct{ err error }

// landStarted is the answer to starting a landing.
type landStarted struct {
	worktree string
	id       string
	err      error
}

func landingRunning(w api.WorktreeStatus) bool {
	return w.Landing != nil && w.Landing.State != api.WorktreeLandingStateLanded && w.Landing.State != api.WorktreeLandingStateOpened && w.Landing.State != api.WorktreeLandingStateFailed
}

// landingFailed is the failure of the last landing, while it still says something
// of the worktree: its commits are still ahead.
func landingFailed(w api.WorktreeStatus) bool {
	return w.Landing != nil && w.Landing.State == api.WorktreeLandingStateFailed && w.Ahead != nil && *w.Ahead > 0
}

// landingSummary is what a worktree's row says of its landing; "" when nothing.
func landingSummary(w api.WorktreeStatus) string {
	switch {
	case landingRunning(w):
		switch w.Landing.State {
		case api.WorktreeLandingStateRebasing:
			return "landing: rebase"
		case api.WorktreeLandingStateChecking:
			return "landing: check"
		case api.WorktreeLandingStatePushing:
			return "landing: push"
		case api.WorktreeLandingStateOpening:
			return "landing: PR"
		}
		return "landing"
	case landingFailed(w):
		switch deref(w.Landing.Code) {
		case string(api.ErrorCodeRebaseFailed):
			return "rebase conflict"
		case string(api.ErrorCodeCheckFailed):
			return "check failed"
		case string(api.ErrorCodeBaseMoved):
			return "base moved"
		case string(api.ErrorCodePushFailed):
			return "push failed"
		case string(api.ErrorCodePrFailed):
			return "PR failed"
		}
		return "not landed"
	}
	return ""
}

// landingShort is landingSummary where the row has little room.
func landingShort(w api.WorktreeStatus) string {
	switch s := landingSummary(w); s {
	case "landing: rebase", "landing: check", "landing: push", "landing: PR":
		return "landing"
	case "rebase conflict":
		return "conflict"
	default:
		return s
	}
}

// pullRequestSummary is what a row says of the worktree's PR; "" when none is known.
func pullRequestSummary(w api.WorktreeStatus) string {
	if w.PullRequest == nil {
		return ""
	}
	return fmt.Sprintf("PR #%d %s", w.PullRequest.Number, w.PullRequest.State)
}

// pullRequestHolds reports whether the worktree's PR is at the commit it is on, so
// that landing again would add nothing.
func pullRequestHolds(w api.WorktreeStatus, state api.WorktreePullRequestState) bool {
	return w.PullRequest != nil && w.PullRequest.State == state && w.Head != nil && w.PullRequest.Head == *w.Head
}

// mergedThroughPR reports whether nothing keeps the worktree from counting as landed
// but commits that a PR took there, which a squash leaves looking ahead. The daemon
// asks gh again before removing it.
func mergedThroughPR(w api.WorktreeStatus) bool {
	return pullRequestHolds(w, api.WorktreePullRequestStateMerged) && len(w.Files) == 0
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (m *Model) worktreeNamed(name string) (api.WorktreeStatus, bool) {
	list := m.worktreeStatuses()
	i := slices.IndexFunc(list, func(w api.WorktreeStatus) bool { return w.Name == name })
	if i < 0 {
		return api.WorktreeStatus{}, false
	}
	return list[i], true
}

// landBarKey is what the bar says of L on the worktree's tab; nothing with nothing to
// land.
func (m *Model) landBarKey(name string) (barKey, bool) {
	w, ok := m.worktreeNamed(name)
	if !ok || name == "main" {
		return barKey{}, false
	}
	if _, slot := m.slotAt(w.Path); slot {
		return barKey{}, false // its plan lands it
	}
	switch {
	case landingRunning(w):
		return barKey{landKey, "landing…", 8}, true
	case len(w.Files) > 0:
		return barKey{landKey, commitFirst, 8}, true
	case w.Ahead == nil || *w.Ahead == 0 || w.Base == nil || mergedThroughPR(w):
		return barKey{}, false
	case pullRequestHolds(w, api.WorktreePullRequestStateOpen):
		return barKey{landKey, pullRequestSummary(w), 8}, true
	}
	return barKey{landKey, "land", 8}, true
}

// askLand starts what L does on a worktree's tab, or says why it does nothing.
func (m *Model) askLand(tab string, v *diffView) tea.Cmd {
	name, file := diffTabOf(tab)
	if file != "" {
		return nil
	}
	if s, ok := m.slotNamed(name); ok {
		v.notice = fmt.Sprintf("%s is a slot of plan %s, which lands it on %s", name, s.plan.Name, planBranch(s.plan.Name))
		return nil
	}
	key, ok := m.landBarKey(name)
	if !ok {
		return nil
	}
	if key.label != "land" {
		v.notice = key.label
		return nil
	}
	w, _ := m.worktreeNamed(name)
	if agents := m.workingAgentsIn(name); len(agents) > 0 {
		v.notice = strings.Join(agents, ", ") + " is working here: land once it stops"
		return nil
	}
	if landingFailed(w) && deref(w.Landing.Code) == string(api.ErrorCodeRebaseFailed) && deref(w.Landing.Head) == deref(w.Head) {
		m.askRebase(w, v)
		return nil
	}
	f := m.following
	ref := "id:" + f.project.Id
	return func() tea.Msg {
		s, err := m.client.ProjectLanding(m.ctx, ref)
		return landSettingsRead{worktree: name, settings: s, err: err}
	}
}

// workingAgentsIn names the agents working in the worktree: it does not land under
// them.
func (m *Model) workingAgentsIn(name string) []string {
	var names []string
	for _, a := range m.runningAgentsIn(name) {
		if a.Activity != nil && *a.Activity == api.AgentActivityWorking {
			names = append(names, a.Name)
		}
	}
	return names
}

// askRebase opens the box that asks an agent to do the rebase wdx could not.
func (m *Model) askRebase(w api.WorktreeStatus, v *diffView) {
	agents, chosen := m.agentsToAsk(w.Name)
	if len(agents) == 0 {
		v.notice = "rebase conflict, and no agent is running in this project"
		return
	}
	branch := w.Name
	if w.Branch != nil {
		branch = *w.Branch
	}
	m.commit = &commitBox{worktree: w.Name, agents: agents, chosen: chosen, rebase: w.Landing.Conflicts,
		text: "Rebase " + branch + " onto " + w.Landing.Base + " in " + w.Path + ", resolving the conflicts"}
	m.log.Info("rebase asked", "worktree", w.Name, "conflicts", len(w.Landing.Conflicts))
}

func (m *Model) landSettingsRead(msg landSettingsRead) {
	if msg.err != nil {
		m.statusNotice = msg.err.Error()
		return
	}
	s := msg.settings
	m.land = &landBox{worktree: msg.worktree, method: s.Method, check: deref(s.Check), push: s.Push, editing: !s.Configured}
	m.log.Info("landing asked", "worktree", msg.worktree, "configured", s.Configured)
}

// landBoxKey takes a key while the box is open.
func (m *Model) landBoxKey(msg tea.KeyPressMsg) tea.Cmd {
	b := m.land
	if b.busy {
		return nil
	}
	name := msg.String()
	if b.editing {
		switch {
		case name == "esc":
			m.land = nil
		case name == "tab":
			b.push = !b.push
		case name == "left" || name == "right":
			b.method = otherMethod(b.method)
		case name == "enter":
			b.busy, b.err = true, ""
			return m.saveLandSettings(b)
		case name == "backspace" && b.check != "":
			r := []rune(b.check)
			b.check = string(r[:len(r)-1])
		case typed(msg):
			b.check += msg.Text
		}
		return nil
	}
	switch name {
	case "y":
		b.busy, b.err = true, ""
		ref := "id:" + m.following.project.Id
		worktree := b.worktree
		m.log.Info("landing confirmed", "worktree", worktree)
		return func() tea.Msg {
			r, err := m.client.LandWorktree(m.ctx, worktree, ref, "", "", "")
			return landStarted{worktree: worktree, id: r.Landing.Id, err: err}
		}
	case "m":
		b.editing = true
	case "n", "esc":
		m.land = nil
	}
	return nil
}

func otherMethod(method api.ProjectLandingMethod) api.ProjectLandingMethod {
	if method == api.ProjectLandingMethodPr {
		return api.ProjectLandingMethodRebase
	}
	return api.ProjectLandingMethodPr
}

func (m *Model) saveLandSettings(b *landBox) tea.Cmd {
	ref := "id:" + m.following.project.Id
	check := strings.TrimSpace(b.check)
	method := api.ProjectLandingUpdateMethod(b.method)
	body := api.ProjectLandingUpdate{Method: &method, Push: b.push}
	if check != "" {
		body.Check = &check
	}
	return func() tea.Msg {
		_, err := m.client.SetProjectLanding(m.ctx, ref, body)
		return landSettingsSaved{err: err}
	}
}

func (m *Model) landSettingsSaved(msg landSettingsSaved) {
	b := m.land
	if b == nil {
		return
	}
	b.busy = false
	if msg.err != nil {
		b.err = msg.err.Error()
		return
	}
	b.editing = false
	b.check = strings.TrimSpace(b.check)
}

// landStarted closes the box, or leaves it open saying why it did not start.
func (m *Model) landStarted(msg landStarted) {
	b := m.land
	if b == nil {
		return
	}
	b.busy = false
	if msg.err != nil {
		m.log.Info("landing refused", "worktree", msg.worktree, "err", msg.err)
		b.err = msg.err.Error()
		return
	}
	m.land = nil
	if l := m.list(); l != nil {
		if l.landAsked == nil {
			l.landAsked = map[string]string{}
		}
		l.landAsked[msg.worktree] = msg.id
	}
	m.landingsChanged() // the stream may have said how it ended before this answer came
}

// landingsChanged says how a landing this tide started has ended, and offers to
// remove what landed. One started elsewhere shows on its row only.
func (m *Model) landingsChanged() {
	l := m.list()
	if l == nil {
		return
	}
	for _, w := range m.worktreeStatuses() {
		if w.Landing == nil || l.landAsked[w.Name] != w.Landing.Id || landingRunning(w) {
			continue
		}
		delete(l.landAsked, w.Name)
		if w.Landing.State == api.WorktreeLandingStateFailed {
			m.statusNotice = landingFailure(w)
			m.log.Info("landing failed", "worktree", w.Name, "code", deref(w.Landing.Code))
			continue
		}
		if w.Landing.State == api.WorktreeLandingStateOpened {
			m.statusNotice = w.Name + ": PR open"
			if pr := w.Landing.PullRequest; pr != nil {
				m.statusNotice = fmt.Sprintf("%s: PR #%d open, %s", w.Name, pr.Number, pr.Url)
			}
			m.log.Info("pull request opened", "worktree", w.Name)
			continue
		}
		m.statusNotice = "landed " + w.Name + " on " + w.Landing.Base
		if w.Landing.PushError != nil {
			m.statusNotice += ", not pushed: " + *w.Landing.PushError
		} else if w.Landing.Pushed {
			m.statusNotice += ", pushed"
		}
		m.log.Info("landed", "worktree", w.Name)
		if m.confirm == nil && m.commit == nil && m.comment == nil && m.agentForm == nil && m.land == nil && !m.menu && !m.popupOpen() {
			r := m.worktreeRemovalOf(w)
			m.confirm = &confirmBox{r: r}
		}
	}
}

// landingFailure is the one line saying what to do about a landing that failed.
func landingFailure(w api.WorktreeStatus) string {
	switch deref(w.Landing.Code) {
	case string(api.ErrorCodeRebaseFailed):
		if n := len(w.Landing.Conflicts); n > 0 {
			return fmt.Sprintf("%s: rebase stopped on a conflict in %s, undone: L asks an agent", w.Name, filesOf(n))
		}
	case string(api.ErrorCodeBaseMoved):
		return w.Name + ": " + w.Landing.Base + " moved during the check: L rebases and checks again"
	}
	return w.Name + ": " + deref(w.Landing.Detail)
}

func filesOf(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// landPopup is what the box shows.
func (m *Model) landPopup() popup {
	b, st := m.land, m.st
	check := st.Muted("none")
	if b.check != "" {
		check = st.Command(b.check)
	}
	pushed := "no"
	if b.push {
		pushed = "yes"
	}
	if b.editing {
		body := []string{"How this project lands, for every worktree:", "", "Method: " + st.Muted("< ") + methodName(b.method) + st.Muted(" >"), "", "Check to run first, empty for none:", "> " + b.check + "_"}
		if b.method != api.ProjectLandingMethodPr {
			body = append(body, "", "Push the base to origin: "+pushed)
		}
		if b.err != "" {
			body = append(body, "", st.Failure(b.err))
		}
		return popup{
			title:  "Landing settings",
			body:   body,
			footer: st.Command("Enter") + " save  " + st.Command("← →") + " method  " + st.Command("Tab") + " push  " + st.Command("Esc") + " cancel",
		}
	}
	w, _ := m.worktreeNamed(b.worktree)
	base := deref(w.Base)
	lines := "↑" + strconv.Itoa(aheadOf(w))
	if w.Added != nil && w.Removed != nil {
		lines += fmt.Sprintf(" +%d −%d", *w.Added, *w.Removed)
	}
	byPR := b.method == api.ProjectLandingMethodPr
	question := "Land " + st.Name(b.worktree) + " on " + st.Name(base) + "?"
	if byPR {
		question = "Open a PR of " + st.Name(b.worktree) + " onto " + st.Name(strings.TrimPrefix(base, "origin/")) + "?"
	}
	body := []string{question, lines}
	if s := verifiedProgress(w); s != "" {
		body = append(body, s)
	}
	if n := len(m.draftComments(b.worktree)); n > 0 {
		body = append(body, st.Warning(countOf(n)+" not sent"))
	}
	if byPR {
		body = append(body, "", "Pushes the branch, and adds to a PR already open.", "Check: "+check)
	} else {
		body = append(body, "", "Rebases first if "+base+" has moved on.", "Check: "+check, "Push: "+pushed)
	}
	for _, a := range m.agents() {
		if !byPR && a.State == api.AgentStateRunning && a.Activity != nil && *a.Activity == api.AgentActivityWorking && m.worktreeOf(a) != b.worktree && m.baseCheckoutHolds(a, base) {
			body = append(body, st.Warning(a.Name+" is working in "+base+": its files move, its changes stay"))
		}
	}
	if b.busy {
		body = append(body, "", st.Muted("starting…"))
	}
	if b.err != "" {
		body = append(body, "", st.Failure(b.err))
	}
	action := " land  "
	if byPR {
		action = " open PR  "
	}
	return popup{
		title:  "Land worktree",
		body:   body,
		footer: st.Command("y") + action + st.Command("m") + " change settings  " + st.Command("n") + " or " + st.Command("Esc") + " cancel",
	}
}

func methodName(method api.ProjectLandingMethod) string {
	if method == api.ProjectLandingMethodPr {
		return "open a PR"
	}
	return "rebase and move the base"
}

func aheadOf(w api.WorktreeStatus) int {
	if w.Ahead == nil {
		return 0
	}
	return *w.Ahead
}

// baseCheckoutHolds reports whether the agent works in the checkout that has base.
func (m *Model) baseCheckoutHolds(a api.Agent, base string) bool {
	where := m.worktreeOf(a)
	return slices.ContainsFunc(m.worktreeStatuses(), func(w api.WorktreeStatus) bool {
		return w.Name == where && w.Branch != nil && *w.Branch == base
	})
}
