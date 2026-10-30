package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/api"
)

// event is one event of a project stream as the test read it.
type event struct {
	name    string
	project api.Project
	data    string
}

// follow opens the stream of the project ref and returns its events, which
// end when the stream does; closing the returned function leaves it.
func (td *testDaemon) follow(t *testing.T, ref string) (<-chan event, func()) {
	t.Helper()
	ctx, leave := context.WithCancel(context.Background())
	resp, err := td.client.StreamProject(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %s %s", resp.Status, resp.Header.Get("Content-Type"))
	}
	events := make(chan event, 100)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		var e event
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				e.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				e.data = strings.TrimPrefix(line, "data: ")
			case line == "":
				if e.name == "project" {
					var v api.ProjectEnvelope
					_ = json.Unmarshal([]byte(e.data), &v)
					e.project = v.Project
				}
				events <- e
				e = event{}
			}
		}
	}()
	t.Cleanup(leave)
	return events, leave
}

// waitEvent returns the first event want accepts: changes close together
// may come as one event, so a test waits for a state, not a count.
func waitEvent(t *testing.T, events <-chan event, what string, want func(event) bool) event {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case e, ok := <-events:
			if !ok {
				t.Fatalf("the stream ended waiting for %s", what)
			}
			if want(e) {
				return e
			}
		case <-timeout:
			t.Fatalf("no event with %s", what)
		}
	}
}

func terminalsIn(e event) []api.Terminal {
	if e.project.Terminals == nil {
		return nil
	}
	return *e.project.Terminals
}

func TestStreamProject(t *testing.T) {
	t.Parallel()
	td := start(t)
	p := td.create(t, "api")
	events, _ := td.follow(t, "api")

	first := waitEvent(t, events, "the project", func(e event) bool { return e.name == "project" })
	if first.project.Id != p.Id || first.project.Terminals == nil || len(*first.project.Terminals) != 0 || first.project.Worktrees[0].Present == nil {
		t.Fatalf("first event: %+v", first.project)
	}

	// Every change under the project sends it whole again.
	term := td.createTerminal(t, "api", p.Worktrees[0].Path, "shell")
	waitEvent(t, events, "a running terminal", func(e event) bool {
		ts := terminalsIn(e)
		return len(ts) == 1 && ts[0].Name == "shell" && ts[0].State == api.TerminalStateRunning
	})

	// A shell exiting by itself changes no request's state: the store says so.
	if err := syscall.Kill(*term.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, events, "the terminal exited", func(e event) bool {
		ts := terminalsIn(e)
		return len(ts) == 1 && ts[0].State == api.TerminalStateExited
	})

	if r, err := td.client.DeleteTerminalWithResponse(context.Background(), "id:"+term.Id, nil); err != nil || r.JSON200 == nil {
		t.Fatalf("remove terminal: %v %s", err, r.Body)
	}
	waitEvent(t, events, "no terminal", func(e event) bool { return len(terminalsIn(e)) == 0 })

	for _, state := range []api.ProjectState{api.ProjectStateClosed, api.ProjectStateOpen} {
		if r, err := td.client.UpdateProjectWithResponse(context.Background(), "api", api.UpdateProject{State: state}); err != nil || r.JSON200 == nil {
			t.Fatalf("%s: %v %s", state, err, r.Body)
		}
		waitEvent(t, events, string(state), func(e event) bool { return e.project.State == state })
	}

	// Removed, it says so and ends.
	if r, err := td.client.DeleteProjectWithResponse(context.Background(), "api", nil); err != nil || r.JSON200 == nil {
		t.Fatalf("remove project: %v %s", err, r.Body)
	}
	removed := waitEvent(t, events, "removed", func(e event) bool { return e.name == "removed" })
	if removed.data != `{"project_id":"`+p.Id+`"}` {
		t.Fatalf("removed event: %s", removed.data)
	}
	select {
	case e, ok := <-events:
		if ok {
			t.Fatalf("an event after removed: %+v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end after removed")
	}
}

func TestStreamUnchangedSendsNothing(t *testing.T) {
	t.Parallel()
	td := start(t)
	td.create(t, "api")
	td.create(t, "web")
	events, leave := td.follow(t, "id:"+td.create(t, "docs").Id)
	waitEvent(t, events, "the project", func(e event) bool { return e.name == "project" })

	// A change to another project sends nothing on this one's stream.
	if r, err := td.client.UpdateProjectWithResponse(context.Background(), "web", api.UpdateProject{State: api.ProjectStateClosed}); err != nil || r.JSON200 == nil {
		t.Fatalf("close web: %v %s", err, r.Body)
	}
	select {
	case e := <-events:
		t.Fatalf("an event for another project's change: %+v", e)
	case <-time.After(200 * time.Millisecond):
	}
	leave() // the stream ends when the client leaves: checked as logged
}

func TestStreamUnknownProject(t *testing.T) {
	t.Parallel()
	td := start(t)
	resp, body := td.do(t, http.MethodGet, "/v1/projects/nope/events", "")
	if resp.StatusCode != http.StatusNotFound || body["code"] != string(api.ErrorCodeNotFound) {
		t.Fatalf("got %s %v", resp.Status, body)
	}
}

func TestStreamEndsWhenDaemonStops(t *testing.T) {
	t.Parallel()
	td := start(t)
	td.create(t, "api")
	events, _ := td.follow(t, "api")
	waitEvent(t, events, "the project", func(e event) bool { return e.name == "project" })

	stopped := time.Now()
	td.stop()
	if _, ok := <-events; ok {
		t.Fatal("an event after the daemon stopped")
	}
	// Shutdown waits for the requests in flight, a stream among them.
	if waited := time.Since(stopped); waited > 5*time.Second {
		t.Fatalf("the daemon took %s to stop with a stream open", waited)
	}
}

func TestNoticesCollapse(t *testing.T) {
	t.Parallel()
	n := newNotices()
	c, stop := n.subscribe()
	for range 3 {
		n.notify()
	}
	<-c
	select {
	case <-c:
		t.Fatal("three notices made more than one")
	default:
	}
	stop()
	n.notify() // to no one
	select {
	case <-c:
		t.Fatal("a notice after stop")
	default:
	}
}
