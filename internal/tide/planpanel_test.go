package tide

import (
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/style"
	"github.com/srjn45/wdx/internal/waittest"
)

// panelText is the panel of v's cursor drawn columns × lines, without color or
// trailing spaces.
func panelText(v *planView, wide bool, columns, lines int) string {
	it, _ := v.cursorItem()
	_, now := ssoRun()
	got := v.panel(it, wide, columns, lines, now, style.New(false))
	for i, l := range got {
		got[i] = strings.TrimRight(ansi.Strip(l), " ")
	}
	return strings.Join(got, "\n")
}

// TestPlanPanel: the panel says the task's state and time, its agent, slot,
// check's terminal and conflicts, what it needs and unblocks, its prompt and
// a done one's summary sharing the room, and what Enter does.
func TestPlanPanel(t *testing.T) {
	t.Parallel()
	v, _ := ssoRun()
	v.cursor = "schema"
	const doneWide = `✓ done · 4m
claude-a1

Unblocks
  ✓ oidc-client
  ✓ docs

Prompt
  Add the sessions table.

Summary
  Added sessions, with an index
  on its user.

Enter read it`
	if got := panelText(v, true, 33, 20); got != doneWide {
		t.Errorf("a done task, wide: got\n%s\nwant\n%s", got, doneWide)
	}
	const doneShort = `✓ done · 4m
claude-a1

Unblocks
  ✓ oidc-client
  ✓ docs

Prompt
  Add the sessions table.

Summary
  Added sessions, with an …+1

Enter read it`
	if got := panelText(v, true, 33, 14); got != doneShort {
		t.Errorf("a done task, short: got\n%s\nwant\n%s", got, doneShort)
	}
	const doneNarrow = `✓ done · 4m · claude-a1
Unblocks ✓ oidc-client, ✓ docs
Add the sessions table.
Summary: Added sessions, with an index on its user.
Enter read it`
	if got := panelText(v, false, 60, 5); got != doneNarrow {
		t.Errorf("a done task, narrow: got\n%s\nwant\n%s", got, doneNarrow)
	}

	tasks := *v.content.Definition.Tasks
	rebase, slot := api.PlanTaskFailureRebase, "/r/.worktrees/sso-login-2"
	tasks[3].State, tasks[3].Failure, tasks[3].SlotPath, tasks[3].Conflicts = api.PlanTaskStateLandFailed, &rebase, &slot, &[]string{"a.go", "b.go"}
	v.cursor = "callback"
	const landFailed = `⚠ land failed: rebase · 6m · claude-c3 · slot sso-login-2 · conflicts a.go, b.go
Needs ✓ oidc-client · Unblocks ○ e2e (also after ui-button)
Handle the IdP's callback.
Enter answer it`
	if got := panelText(v, false, 90, 4); got != landFailed {
		t.Errorf("a task that did not land, narrow: got\n%s\nwant\n%s", got, landFailed)
	}
}

// TestPlanPanelOfPlans: the panel of a plan of plans' plan says its state and
// progress, what it needs and unblocks, its goal once read, and why it waits:
// for the person, or for the plans before it.
func TestPlanPanelOfPlans(t *testing.T) {
	t.Parallel()
	v, _ := authRun()
	got := panelText(v, true, 40, 20)
	for _, want := range []string{"○ waiting for you · 3 tasks", "Needs\n  ✓ sso-login", "Unblocks\n  ○ cleanup, also after audit",
		"Goal\n  Ask for a second factor when a user", "r starts it, once sso-login is in the\ndefault branch", "Enter open it"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
	v.cursor = "cleanup"
	if got := panelText(v, true, 40, 20); !strings.Contains(got, "it starts once mfa and audit are done") || !strings.Contains(got, "Goal\n  loading…") {
		t.Errorf("a plan waiting for others:\n%s", got)
	}
	v.cursor = "audit"
	if got := panelText(v, true, 40, 20); !strings.Contains(got, "● running · 2/5") || strings.Contains(got, "starts") {
		t.Errorf("a plan running:\n%s", got)
	}
}

// TestFitBlocks: blocks share the room, the first first, each cut short ending
// with how many of its lines are left out.
func TestFitBlocks(t *testing.T) {
	t.Parallel()
	blocks := []panelBlock{{text: "one two three four five six"}, {text: "a b c d e f"}}
	for _, c := range []struct {
		room int
		want [][]string
	}{
		{10, [][]string{{"one two", "three four", "five six"}, {"a b c d e", "f"}}},
		{4, [][]string{{"one two", "three …+1"}, {"a b c d e", "f"}}},
		{3, [][]string{{"one two", "three …+1"}, {"a b c …+1"}}},
		{1, [][]string{{"one …+2"}, {}}},
	} {
		got := fitBlocks(blocks, 10, c.room)
		for i := range got {
			if !slices.Equal(got[i], c.want[i]) {
				t.Errorf("in %d lines, block %d: %q, want %q", c.room, i, got[i], c.want[i])
			}
		}
	}
}

// TestPlanTabReading: Enter on a task done, pending or skipped shows its whole
// prompt and summary in place of the tab, which ↑ ↓ scroll and Esc leaves; the
// list is as it was.
func TestPlanTabReading(t *testing.T) {
	t.Parallel()
	m := plansModel(t)
	v, now := ssoRun()
	m.now = func() time.Time { return now }
	tasks := *v.content.Definition.Tasks
	tasks[1].State = api.PlanTaskStateSkipped
	long := strings.Repeat("A line of the prompt.\n", 30) + "The last line."
	tasks[0].Prompt = long
	m.moveToPlan(4) // sso
	keys(m, "enter")
	loadPlan(m, "doc-sso", *v.content)
	keys(m, "alt+right")
	tab := m.list().planViews[planTab("doc-sso")]
	for _, id := range []string{"schema", "oidc-client", "e2e"} {
		tab.cursor = id
		keys(m, "enter")
		if tab.reading != id {
			t.Fatalf("Enter on %s: reading %q", id, tab.reading)
		}
		wantText(t, m, id+"  ", "Prompt", "Esc back")
		keys(m, "esc")
		if tab.reading != "" || tab.cursor != id {
			t.Errorf("Esc on %s: reading %q, cursor %q", id, tab.reading, tab.cursor)
		}
	}

	tab.cursor = "schema"
	keys(m, "enter")
	wantText(t, m, "✓ done · 4m · claude-a1", "Esc back  ↑↓ scroll", "A line of the prompt.")
	if strings.Contains(text(m), "The last line.") || strings.Contains(text(m), "Added sessions") {
		t.Fatalf("the end shows before scrolling:\n%s", text(m))
	}
	keys(m, "down")
	if tab.readOffset != 1 {
		t.Errorf("↓ scrolled to %d", tab.readOffset)
	}
	keys(m, "pgdown", "pgdown", "pgdown", "pgdown", "down")
	wantText(t, m, "The last line.", "Summary", "Added sessions, with an index on its user.")
	at := tab.readOffset
	keys(m, "up")
	if tab.readOffset != at-1 {
		t.Errorf("↑ scrolled from %d to %d", at, tab.readOffset)
	}

	// A new revision without the task goes back to the list.
	c := *v.content
	rest := slices.Clone(tasks[1:])
	for i := range rest {
		rest[i].After = slices.DeleteFunc(slices.Clone(rest[i].After), func(a string) bool { return a == "schema" })
	}
	c.Definition.Tasks, c.Revision = &rest, 4
	loadPlan(m, "doc-sso", c)
	wantText(t, m, "─ Tasks ")
	if tab.reading != "" {
		t.Errorf("reading %q, gone", tab.reading)
	}
}

// goalsTo points m's client at a daemon of its own that answers a plan's read
// with a goal saying its path and revision, and sends on each path asked.
func goalsTo(t *testing.T, m *Model) <-chan string {
	t.Helper()
	socket := filepath.Join(runtest.Dir(t), "wdx.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	asked := make(chan string, 8)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/v1/plans/") {
			http.NotFound(w, r)
			return
		}
		asked <- r.URL.Path + " r" + r.URL.Query().Get("revision")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.PlanContent{Definition: api.PlanDefinition{Goal: strings.TrimPrefix(r.URL.Path, "/v1/plans/id:") + " at r" + r.URL.Query().Get("revision"), Constraints: []string{}}})
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { srv.Close() })
	m.client = client.New(client.Options{Socket: socket, DebugID: "dbg-0123456789ab"})
	return asked
}

// nextAsked is the next path the daemon of goalsTo was asked for, until the
// test's deadline.
func nextAsked(t *testing.T, asked <-chan string) string {
	t.Helper()
	select {
	case got := <-asked:
		return got
	case <-time.After(time.Until(waittest.Deadline(t))):
		t.Fatal("nothing asked")
		return ""
	}
}

// TestPlanOfPlansGoal: the panel of a plan of plans reads the goal of the plan
// under the cursor once a revision, and again on a new one.
func TestPlanOfPlansGoal(t *testing.T) {
	t.Parallel()
	m := runningParent(t)
	asked := goalsTo(t, m)
	wantText(t, m, "loading…")
	_, cmd := m.Update(nil)
	run(m, cmd)
	if got := nextAsked(t, asked); got != "/v1/plans/id:doc-b r1" {
		t.Errorf("first asked %q", got)
	}
	wantText(t, m, "doc-b at r1")
	keys(m, "down")
	if got := nextAsked(t, asked); got != "/v1/plans/id:doc-a r1" {
		t.Errorf("then asked %q", got)
	}
	keys(m, "up", "down", "up")
	b := m.plans()[2]
	b.Revision = 2
	setPlans(m, m.plans()[0], m.plans()[1], b, m.plans()[3])
	wantText(t, m, "doc-b at r1")
	keys(m, "down", "up")
	if got := nextAsked(t, asked); got != "/v1/plans/id:doc-b r2" {
		t.Errorf("on a new revision asked %q", got)
	}
	wantText(t, m, "doc-b at r2")
	select {
	case got := <-asked:
		t.Errorf("asked again at the same revision: %q", got)
	default:
	}
}
