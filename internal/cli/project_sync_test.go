package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/gittest"
)

// remoteProject opens a project named name from a repository with a git
// remote, whose key the hub files its docs under, and returns its directory.
func remoteProject(t *testing.T, home, name string) string {
	t.Helper()
	dir := gittest.Repo(t, name)
	gittest.Git(t, dir, "remote", "add", "origin", "https://example.com/o/"+name+".git")
	if v, exit := wdxJSON(t, home, "project", "open", "--local", dir); exit != clikit.ExitOK {
		t.Fatalf("open %s: %+v", name, v)
	}
	return dir
}

func wantSync(t *testing.T, what string, v jsonOutput, exit int, result string, on bool, pending int) {
	t.Helper()
	if exit != clikit.ExitOK || v.Result != result || v.Sync == nil || v.Sync.On != on || v.Sync.Pending != pending {
		t.Fatalf("%s: exit %d, %+v, sync %+v", what, exit, v, v.Sync)
	}
}

func TestProjectSyncOn(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	dir := remoteProject(t, home, "app")
	newDoc(t, home, dir, "notes", "one")
	newDoc(t, home, dir, "design", "two")

	v, exit := wdxJSON(t, home, "project", "sync", "--name", "app")
	wantSync(t, "before", v, exit, "", false, 0)
	if v.Sync.RepoKey == nil || *v.Sync.RepoKey != "example.com/o/app" || v.Sync.LoggedIn || v.Sync.LastPushedAt != nil {
		t.Fatalf("status: %+v", v.Sync)
	}
	v, exit = wdxJSON(t, home, "project", "sync", "--name", "app", "--on")
	wantSync(t, "on", v, exit, "changed", true, 2)
	v, exit = wdxJSON(t, home, "project", "sync", "--name", "app", "--on")
	wantSync(t, "on again", v, exit, "unchanged", true, 2)
	newDoc(t, home, dir, "todo", "three")
	v, exit = wdxJSON(t, home, "project", "sync", "--name", "app")
	wantSync(t, "a doc made", v, exit, "", true, 3)
}

func TestProjectSyncOff(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	dir := remoteProject(t, home, "app")
	newDoc(t, home, dir, "notes", "one")
	wdxJSON(t, home, "project", "sync", "--name", "app", "--on")

	v, exit := wdxJSON(t, home, "project", "sync", "--name", "app", "--off")
	wantSync(t, "off", v, exit, "changed", false, 0)
	v, exit = wdxJSON(t, home, "project", "sync", "--name", "app", "--off")
	wantSync(t, "off again", v, exit, "unchanged", false, 0)
}

func TestProjectSyncNoRemote(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	newProject(t, home, "app")
	v, exit := wdxJSON(t, home, "project", "sync", "--name", "app", "--on")
	wantError(t, v, exit, api.ErrorCodeNoRemote, clikit.ExitRejected)
	wantHint(t, v, "git remote add origin")
}

func TestProjectSyncNotFound(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	v, exit := wdxJSON(t, home, "project", "sync", "--name", "nowhere")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	v, exit = wdxJSON(t, home, "project", "sync", "--name", "nowhere", "--on", "--off")
	wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)
}

func TestProjectSyncText(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	dir := remoteProject(t, home, "app")
	newDoc(t, home, dir, "notes", "one")
	out := wdx(t, home, "project", "sync", "--name", "app", "--on")
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "sync turned on\nsync on, 1 change waiting, never pushed, repository example.com/o/app\n") ||
		!strings.Contains(out.Stdout, "hint: not logged in: changes wait until wdx hub login") {
		t.Fatalf("text: %+v", out)
	}
}

// docsOnHub waits for the hub's doc id of hh's user to be as ok wants.
func docsOnHub(t *testing.T, hh *hubHome, what, id string, ok func(name string, revision int, err error) bool) {
	t.Helper()
	var name string
	var revision int
	var err error
	if !eventually(t, func() bool {
		d, e := hh.hub.Server.Docs().Doc(context.Background(), hh.hub.User.ID, id)
		name, revision, err = d.Name, d.Revision, e
		return ok(name, revision, err)
	}) {
		t.Fatalf("%s: the hub has %q at revision %d, %v", what, name, revision, err)
	}
}

// TestProjectSyncEndToEnd follows a doc created, revised, renamed and removed
// on a node to the hub; its text is in no line of either's logs.
func TestProjectSyncEndToEnd(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	dir := remoteProject(t, hh.home, "app")
	text := "the plan for the launch, which no log may show"
	hh.secret(text)
	d := newDoc(t, hh.home, dir, "launch", text)
	if v, exit := wdxJSON(t, hh.home, "project", "sync", "--name", "app", "--on"); exit != clikit.ExitOK {
		t.Fatalf("on: %+v", v)
	}
	hh.loggedIn("desktop")
	docsOnHub(t, hh, "created", d.Id, func(name string, rev int, err error) bool { return err == nil && rev == 1 })
	if b, err := hh.hub.Server.Docs().Content(context.Background(), hh.hub.User.ID, d.Id, 1); err != nil || string(b) != text {
		t.Fatalf("hub content %q, %v", b, err)
	}

	second := "the second revision, which no log may show either"
	hh.secret(second)
	if v, exit := wdxJSONWith(t, hh.home, both(in(dir), stdin(second)), "doc", "update", "--name", "launch", "--path", "-", "--base", "1"); exit != clikit.ExitOK {
		t.Fatalf("update: %+v", v)
	}
	docsOnHub(t, hh, "revised", d.Id, func(name string, rev int, err error) bool { return err == nil && rev == 2 })
	if v, exit := wdxJSONWith(t, hh.home, in(dir), "doc", "rename", "--name", "launch", "--new-name", "launched"); exit != clikit.ExitOK {
		t.Fatalf("rename: %+v", v)
	}
	docsOnHub(t, hh, "renamed", d.Id, func(name string, rev int, err error) bool { return err == nil && name == "launched" })
	if v, exit := wdxJSONWith(t, hh.home, in(dir), "doc", "remove", "--name", "launched", "--yes"); exit != clikit.ExitOK {
		t.Fatalf("remove: %+v", v)
	}
	docsOnHub(t, hh, "removed", d.Id, func(_ string, _ int, err error) bool { return err != nil })

	v, exit := wdxJSON(t, hh.home, "project", "sync", "--name", "app")
	if exit != clikit.ExitOK || v.Sync.Pending != 0 || v.Sync.LastPushedAt == nil || !v.Sync.LoggedIn {
		t.Fatalf("status: %+v", v.Sync)
	}
}

// TestProjectSyncFailed checks a doc the hub refuses is listed with its code
// and the hint to rename it, which pushes it.
func TestProjectSyncFailed(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	dir := remoteProject(t, hh.home, "app")
	// Another node of the user made a doc of the name in the repository.
	laptop := hh.hub.NewNode(hh.hub.User, "laptop")
	hh.hub.PushDoc(laptop, "doc-laptop1", "example.com/o/app", "notes", "theirs")

	d := newDoc(t, hh.home, dir, "notes", "mine")
	wdxJSON(t, hh.home, "project", "sync", "--name", "app", "--on")
	hh.loggedIn("desktop")
	var v jsonOutput
	if !eventually(t, func() bool {
		v, _ = wdxJSON(t, hh.home, "project", "sync", "--name", "app")
		return len(v.Sync.Failed) == 1
	}) {
		t.Fatalf("no doc failed: %+v", v.Sync)
	}
	if f := v.Sync.Failed[0]; f.DocId != d.Id || f.Name != "notes" || f.Code != "doc_name_taken" || v.Sync.Pending != 0 {
		t.Fatalf("failed: %+v", v.Sync)
	}
	out := wdx(t, hh.home, "project", "sync", "--name", "app")
	if !strings.Contains(out.Stdout, "failed: notes refused by the hub  [doc_name_taken]") || !strings.Contains(out.Stdout, "wdx doc rename --id "+d.Id+" --new-name <name>") {
		t.Fatalf("text: %+v", out)
	}

	if v, exit := wdxJSONWith(t, hh.home, in(dir), "doc", "rename", "--name", "notes", "--new-name", "my-notes"); exit != clikit.ExitOK {
		t.Fatalf("rename: %+v", v)
	}
	docsOnHub(t, hh, "renamed and pushed", d.Id, func(name string, _ int, err error) bool { return err == nil && name == "my-notes" })
}
