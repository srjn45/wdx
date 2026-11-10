package tide

import (
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/style"
)

// remoteModel is newModel on the node desktop, its link opening sessions
// through open, its ticks kept for the test to fire.
func remoteModel(st style.Style, open func(ctx context.Context, code string) error) (*Model, *ticks) {
	m := New(context.Background(), Options{Style: st, Node: "desktop", Link: NewLink(open), SessionLost: 5 * time.Second})
	tk := &ticks{}
	m.tick = tk.tick
	m.projects.loaded = true
	m.show("demo")
	sent(m, "shell", "tests", "server")
	now := time.UnixMilli(0)
	m.now = func() time.Time { return now }
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m, tk
}

// ticks are the waits a model asked for, which the test fires.
type ticks struct{ waits []tickWait }

type tickWait struct {
	d  time.Duration
	fn func(time.Time) tea.Msg
}

func (tk *ticks) tick(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
	tk.waits = append(tk.waits, tickWait{d, fn})
	return nil
}

// of are the waits of d asked for.
func (tk *ticks) of(d time.Duration) []tickWait {
	var waits []tickWait
	for _, w := range tk.waits {
		if w.d == d {
			waits = append(waits, w)
		}
	}
	return waits
}

func problem(code api.ErrorCode, details map[string]any) error {
	p := api.NewProblem(code, string(code))
	if details != nil {
		p.Details = &details
	}
	return &client.ProblemError{Problem: p}
}

func noSession(context.Context, string) error { return errors.New("no session in this test") }

func lastLine(m *Model) string {
	lines := strings.Split(text(m), "\n")
	return lines[len(lines)-1]
}

// TestRemoteBar: a remote TIDE's bar is in the Remote style and names the
// node, and its window's title does; a local one's is as before.
func TestRemoteBar(t *testing.T) {
	t.Parallel()
	st := style.New(true)
	m, _ := remoteModel(st, noSession)
	remote := strings.TrimSuffix(st.Bar("", style.StatusRemote), "\x1b[0m")
	if bar := m.statusLine(); !strings.HasPrefix(bar, remote) {
		t.Errorf("bar %q, want it in the remote style %q", bar, remote)
	}
	if got := lastLine(m); !strings.HasPrefix(got, " desktop › demo · project") {
		t.Errorf("bar %q, want the node named first", got)
	}
	if got := m.View().WindowTitle; got != "wdx tide · desktop" {
		t.Errorf("title %q", got)
	}

	local, _ := newModel(100, 30)
	local.st = st
	if bar := local.statusLine(); strings.HasPrefix(bar, remote) || !strings.HasPrefix(bar, strings.TrimSuffix(st.Bar("", style.StatusLive), "\x1b[0m")) {
		t.Errorf("a local bar %q, want it live, not remote", bar)
	}
	if got := local.View().WindowTitle; got != "TIDE" {
		t.Errorf("a local title %q", got)
	}
}

// TestRemoteSettings: the menu offers no settings through a session, and
// says where they are changed; C-q s opens nothing.
func TestRemoteSettings(t *testing.T) {
	t.Parallel()
	m, _ := remoteModel(style.New(false), noSession)
	m.Update(key('q', tea.ModCtrl))
	if got := text(m); !strings.Contains(got, "s  settings are changed on desktop itself") {
		t.Fatalf("menu:\n%s", got)
	}
	if strings.Contains(m.menuHint(), "s settings") {
		t.Errorf("the bar offers settings: %s", m.menuHint())
	}
	m.Update(key('s', 0))
	if l := m.list(); slices.Contains(l.tabs, settingsTab) || m.settingsShown() {
		t.Fatalf("settings opened through a session: tabs %v", l.tabs)
	}

	local := newAgentsModel(100, 41, demoAgents()...)
	local.Update(key('q', tea.ModCtrl))
	if got := text(local); !strings.Contains(got, "s  settings") || strings.Contains(got, "changed on") {
		t.Fatalf("a local menu:\n%s", got)
	}
}

// TestRemoteResume: --resume shows what it says through a session too, but
// for a settings tab.
func TestRemoteResume(t *testing.T) {
	t.Parallel()
	r := &Resume{Project: "demo", ProjectID: "proj-demo", Focus: "terminal", Zoomed: "terminal", TerminalOpen: true,
		Agent: settingsTab, Tabs: []string{"agent-a", settingsTab}}
	m := New(context.Background(), Options{Style: style.New(false), Node: "desktop", Link: NewLink(noSession), Resume: r})
	m.tick = noTick
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(projectsListed{projects: []api.ProjectSummary{{Id: "proj-demo", Name: "demo", State: api.ProjectStateOpen}}})
	if m.shown != "demo" || m.layout.focus != terminal || m.layout.zoomed != terminal {
		t.Fatalf("shown %q, focus %s, zoomed %s", m.shown, m.layout.focus, m.layout.zoomed)
	}
	if l := m.agentLists["proj-demo"]; !slices.Equal(l.tabs, []string{"agent-a"}) || l.created == settingsTab {
		t.Fatalf("tabs %v, current %q: want the settings tab dropped", l.tabs, l.created)
	}
	if !slices.Equal(r.Tabs, []string{"agent-a", settingsTab}) {
		t.Errorf("the resume handed in was changed: %v", r.Tabs)
	}
}

// TestRemoteTabsInMemory: the tabs a project's event brings, the host
// TIDE's, are not taken, and none are sent.
func TestRemoteTabsInMemory(t *testing.T) {
	t.Parallel()
	m, _ := remoteModel(style.New(false), noSession)
	agentID := "agent-a"
	agents := []api.Agent{{Id: agentID, Name: "a", State: api.AgentStateRunning}}
	m.Update(projectEvent{gen: m.following.gen, event: client.ProjectEvent{
		Project: api.Project{Id: "proj-demo", Name: "demo", Agents: &agents},
		Tabs:    []api.Tab{{AgentId: &agentID}}, TabsRevision: 3,
	}})
	if l := m.list(); l.tabsRead || l.tabsRevision != 0 {
		t.Fatalf("the host's tabs taken: read %v, revision %d", l.tabsRead, l.tabsRevision)
	}
	if m.tabsSync() != nil || m.viewSync() != nil {
		t.Fatal("a remote TIDE sends its tabs or view")
	}
}

// TestRemotePathField: a new project takes a path on the host in place of
// the directory browser, which reads this machine's files.
func TestRemotePathField(t *testing.T) {
	t.Parallel()
	m, _ := remoteModel(style.New(false), noSession)
	m.Update(browse{})
	f, ok := m.openTop().(*fieldScreen)
	if !ok || !strings.Contains(f.prompt, "Absolute path on desktop") {
		t.Fatalf("screen %T %+v", m.openTop(), m.openTop())
	}
	m.Update(tea.PasteMsg{Content: "app"})
	if _, cmd := m.Update(key(tea.KeyEnter, 0)); cmd != nil || !strings.Contains(text(m), "type an absolute path: app means nothing on desktop") {
		t.Fatalf("a relative path:\n%s", text(m))
	}
	f.value = "/srv/app/"
	_, cmd := m.Update(key(tea.KeyEnter, 0))
	msg, ok := cmd().(createProject)
	if !ok {
		t.Fatalf("enter sent %T", cmd())
	}
	if src, _ := msg.req.Source.AsLocalSource(); src.Path != "/srv/app" {
		t.Fatalf("request %+v", src)
	}

	m.opening = nil
	m.Update(browse{create: true})
	m.Update(tea.PasteMsg{Content: "/srv"})
	_, cmd = m.Update(key(tea.KeyEnter, 0))
	m.Update(cmd())
	name, ok := m.openTop().(*fieldScreen)
	if !ok || !strings.Contains(name.prompt, "created in /srv on desktop") {
		t.Fatalf("after the directory: %T %+v", m.openTop(), m.openTop())
	}
	_, cmd = m.Update(key(tea.KeyEscape, 0))
	m.Update(cmd())
	if dir, ok := m.openTop().(*fieldScreen); !ok || dir.busy {
		t.Fatalf("back on the directory: %+v, want it typed into again", m.openTop())
	}
	_, cmd = m.Update(key(tea.KeyEnter, 0))
	m.Update(cmd())
	m.Update(tea.PasteMsg{Content: "new"})
	_, cmd = m.Update(key(tea.KeyEnter, 0))
	created, ok := cmd().(createProject)
	if !ok {
		t.Fatalf("enter sent %T", cmd())
	}
	if src, _ := created.req.Source.AsNewSource(); src.Path == nil || *src.Path != "/srv" || created.req.Name == nil || *created.req.Name != "new" {
		t.Fatalf("request %+v %v", src, created.req.Name)
	}
}

// TestRemoteSessionClosed: session_closed replaces the window with the
// message at once; a code typed opens another session and TIDE goes on with
// the view it had.
func TestRemoteSessionClosed(t *testing.T) {
	t.Parallel()
	var codes []string
	wrong := true
	m, _ := remoteModel(style.New(false), func(_ context.Context, code string) error {
		codes = append(codes, code)
		if wrong {
			return errors.New("the code is wrong")
		}
		return nil
	})
	m.Update(key(tea.KeyDown, tea.ModAlt)) // the terminal pane
	m.Update(key(']', tea.ModCtrl))        // zoomed
	m.Update(linkChanged{err: problem(api.ErrorCodeSessionClosed, map[string]any{"reason": "idle"})})
	got := text(m)
	if !strings.Contains(got, "session to desktop closed") || !strings.Contains(got, "it was idle for too long") || !strings.Contains(got, "code for desktop: _") {
		t.Fatalf("no message:\n%s", got)
	}
	if strings.Contains(got, "[● shell]") {
		t.Fatalf("the panes show under the message:\n%s", got)
	}
	m.Update(clickAt(5, 5))
	m.Update(key(tea.KeyLeft, tea.ModAlt))
	if m.layout.focus != terminal {
		t.Fatalf("the message let the focus move to %s", m.layout.focus)
	}

	for _, r := range "123456" {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, cmd := m.Update(key(tea.KeyEnter, 0))
	m.Update(cmd())
	if !strings.Contains(text(m), "the code is wrong") || m.remote.ended == nil {
		t.Fatalf("a wrong code:\n%s", text(m))
	}
	wrong = false
	m.Update(tea.PasteMsg{Content: "654321"})
	_, cmd = m.Update(key(tea.KeyEnter, 0))
	m.Update(cmd())
	if !slices.Equal(codes, []string{"123456", "654321"}) {
		t.Fatalf("codes %v", codes)
	}
	if m.remote.ended != nil || m.shown != "demo" || m.layout.focus != terminal || m.layout.zoomed != terminal {
		t.Fatalf("after the code: ended %+v, shown %q, focus %s, zoomed %s", m.remote.ended, m.shown, m.layout.focus, m.layout.zoomed)
	}
	sent(m, "shell")
	if got := text(m); !strings.Contains(got, "terminal · shell · zoomed") {
		t.Fatalf("not gone on:\n%s", got)
	}

	// C-q quits from the message.
	m.Update(linkChanged{err: problem(api.ErrorCodeSessionClosed, nil)})
	if !strings.Contains(text(m), "remote access was turned off, or the network failed") {
		t.Fatalf("no reason known:\n%s", text(m))
	}
	_, cmd = m.Update(key('q', tea.ModCtrl))
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("C-q gave %T", cmd())
	}
}

// TestRemoteSessionClosedReasonLater: of the streams that met the session
// closed, the one that knows the host's reason may come after one that does
// not, and its reason replaces the list of every possible one; a stream
// that knows none after it changes nothing.
func TestRemoteSessionClosedReasonLater(t *testing.T) {
	t.Parallel()
	m, _ := remoteModel(style.New(false), noSession)
	m.Update(linkChanged{err: problem(api.ErrorCodeSessionClosed, nil)})
	if got := text(m); !strings.Contains(got, "or the network failed") {
		t.Fatalf("no reason known:\n%s", got)
	}
	m.Update(linkChanged{err: problem(api.ErrorCodeSessionClosed, map[string]any{"reason": "closed"})})
	m.Update(linkChanged{err: problem(api.ErrorCodeSessionClosed, nil)})
	if got := text(m); !strings.Contains(got, "it was closed, on desktop or here") || strings.Contains(got, "or the network failed") {
		t.Fatalf("the host's reason does not show:\n%s", got)
	}
}

// TestRemoteNodeOffline: the node unreachable shows reconnecting on the bar,
// then the message once it stayed so for the wait; a stream opened between
// ends the wait.
func TestRemoteNodeOffline(t *testing.T) {
	t.Parallel()
	m, tk := remoteModel(style.New(false), noSession)
	offline := linkChanged{err: problem(api.ErrorCodeNodeOffline, nil)}
	m.Update(offline)
	if got := lastLine(m); !strings.Contains(got, "reconnecting to desktop…") {
		t.Fatalf("bar %q", got)
	}
	m.Update(linkChanged{err: problem(api.ErrorCodeHubUnreachable, nil)})
	waits := tk.of(5 * time.Second)
	if len(waits) != 1 {
		t.Fatalf("waits %v, want one of the option's 5s", tk.waits)
	}
	m.Update(linkChanged{})
	m.Update(waits[0].fn(time.Time{}))
	if m.remote.ended != nil || strings.Contains(lastLine(m), "reconnecting") {
		t.Fatalf("a stream opened, yet:\n%s", text(m))
	}

	m.Update(offline)
	waits = tk.of(5 * time.Second)
	if len(waits) != 2 {
		t.Fatalf("waits %v", tk.waits)
	}
	m.Update(waits[0].fn(time.Time{})) // the first wait, ended by the stream opened since
	if text(m); m.remote.ended != nil {
		t.Fatal("the message before the wait ended")
	}
	m.Update(waits[1].fn(time.Time{}))
	if got := text(m); !strings.Contains(got, "session to desktop closed") || !strings.Contains(got, "desktop could not be reached for 5s") {
		t.Fatalf("no message after the wait:\n%s", got)
	}
	// A stream opened since, through a session opened elsewhere, ends it.
	m.Update(linkChanged{})
	if m.remote.ended != nil {
		t.Fatal("the message stays with the session back")
	}
}

// TestRemoteStreamBroke: the project's stream breaking once open, which no
// dial reports, shows reconnecting on the bar at once, then the message once
// the host stayed lost for the wait; the stream opening again ends it.
func TestRemoteStreamBroke(t *testing.T) {
	t.Parallel()
	m, tk := remoteModel(style.New(false), noSession)
	broken := projectEvent{gen: m.following.gen, err: io.ErrUnexpectedEOF}
	m.Update(broken)
	if got := lastLine(m); !strings.Contains(got, "reconnecting to desktop…") {
		t.Fatalf("bar %q", got)
	}
	waits := tk.of(5 * time.Second)
	if len(waits) != 1 {
		t.Fatalf("waits %v, want one of the option's 5s", tk.waits)
	}
	m.Update(linkChanged{}) // the stream opened again
	m.Update(waits[0].fn(time.Time{}))
	if m.remote.ended != nil || strings.Contains(lastLine(m), "reconnecting") {
		t.Fatalf("the stream opened again, yet:\n%s", text(m))
	}

	m.Update(projectEvent{gen: m.following.gen, err: io.ErrUnexpectedEOF})
	waits = tk.of(5 * time.Second)
	if len(waits) != 2 {
		t.Fatalf("waits %v", tk.waits)
	}
	m.Update(waits[1].fn(time.Time{}))
	if got := text(m); !strings.Contains(got, "desktop could not be reached for 5s: unexpected EOF") {
		t.Fatalf("no message after the wait:\n%s", got)
	}
}

// TestRemoteLinkFollowed: Init follows the link, and a TIDE that ended stops.
func TestRemoteLinkFollowed(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	link := NewLink(noSession)
	m := New(ctx, Options{Style: style.New(false), Node: "desktop", Link: link})
	link.Dialed(problem(api.ErrorCodeSessionClosed, nil))
	if got, ok := m.watchLink()().(linkChanged); !ok || got.err == nil {
		t.Fatalf("got %+v", got)
	}
	cancel()
	if got := m.watchLink()(); got != nil {
		t.Fatalf("after the end: %+v", got)
	}
}

// TestDialTerminal: TIDE attaches through Options.DialTerminal.
func TestDialTerminal(t *testing.T) {
	t.Parallel()
	dialed := make(chan string, 4)
	m := New(context.Background(), Options{Style: style.New(false), Node: "desktop", Link: NewLink(noSession),
		DialTerminal: func(_ context.Context, id string) (net.Conn, error) {
			dialed <- id
			return nil, errors.New("no host in this test")
		}})
	m.tick = noTick
	m.projects.loaded = true
	m.show("demo")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	sent(m, "shell")
	m.Update(rested{rests: m.rests})
	if m.term == nil {
		t.Fatal("nothing attaching")
	}
	m.Update(m.tryAttach(m.term)())
	select {
	case got := <-dialed:
		if got != "term-shell" {
			t.Fatalf("dialed %q", got)
		}
	default:
		t.Fatal("the attach dialed no terminal through DialTerminal")
	}
	if m.term == nil || m.term.err == nil {
		t.Fatalf("attachment %+v", m.term)
	}
	m.Update(attachedMsg{gen: m.term.gen, err: problem(api.ErrorCodeHolderVersionUnsupported, nil)})
	if !m.term.tooOld {
		t.Fatal("holder_version_unsupported is tried again")
	}
}
