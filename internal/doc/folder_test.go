package doc

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestCleanFolder(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"":                  "/",
		"/":                 "/",
		"//":                "/",
		"hub":               "/hub",
		"/hub/":             "/hub",
		"Hub //Initial":     "/hub/initial",
		" /a_b/\tc-d/ ":     "/a_b/c-d",
		"//x///y//":         "/x/y",
		"/HUB/Initial/Spec": "/hub/initial/spec",
	} {
		if got, err := CleanFolder(in); err != nil || got != want {
			t.Errorf("CleanFolder(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"a.b", "/hub/..", "/x:y", "/é", "/a\\b"} {
		var invalid *InvalidError
		if _, err := CleanFolder(in); !errors.As(err, &invalid) || invalid.What != "folder" {
			t.Errorf("CleanFolder(%q): %v, want an invalid folder", in, err)
		}
	}
}

func TestFolder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.project(t, "wdx")
	top := f.create(t, p, New{Name: "top"})
	if top.Folder != "/" {
		t.Errorf("a doc given no folder is in %q", top.Folder)
	}
	spec := f.create(t, p, New{Name: "spec", Folder: "Hub/Initial/"})
	if spec.Folder != "/hub/initial" {
		t.Errorf("created in %q", spec.Folder)
	}
	if _, err := f.store.Create(ctx, p, New{Name: "bad", Folder: "a.b"}); err == nil {
		t.Error("created in a folder with a dot")
	}

	before := *f.changes
	if _, changed, err := f.store.Set(ctx, spec.ID, Change{Folder: new("/hub/initial/")}); err != nil || changed || *f.changes != before {
		t.Errorf("moved to the folder it is in, spelled otherwise: changed %v, %v", changed, err)
	}
	moved, changed, err := f.store.Set(ctx, top.ID, Change{Folder: new("/a")})
	if err != nil || !changed || moved.Folder != "/a" || moved.Revision != 1 {
		t.Errorf("moved: %+v, changed %v, %v", moved, changed, err)
	}
	if revisions, err := f.store.Revisions(ctx, top.ID); err != nil || len(revisions) != 1 {
		t.Errorf("a move made a revision: %+v, %v", revisions, err)
	}

	list, err := f.store.List(ctx, p, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range list {
		names = append(names, d.Folder+" "+d.Name)
	}
	if want := []string{"/a top", "/hub/initial spec"}; !slices.Equal(names, want) {
		t.Errorf("listed %q, want by folder, then name: %q", names, want)
	}
}

// TestFamilyFolder: a row listing others and the rows it lists share one folder,
// as a plan of plans and its plans do.
func TestFamilyFolder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	plans, _ := f.plans(t)
	p := f.project(t, "wdx")
	create := func(n New) Doc {
		t.Helper()
		d, err := plans.Create(ctx, p, n)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	folderOf := func(d Doc) string {
		t.Helper()
		got, err := plans.Get(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Folder
	}
	a := create(New{Name: "a", Folder: "/x"})
	b := create(New{Name: "b"})
	other := create(New{Name: "other", Folder: "/x"})
	parent := create(New{Name: "big", Folder: "/hub", Content: []byte("1"), Children: []string{a.ID, b.ID}})
	if folderOf(a) != "/hub" || folderOf(b) != "/hub" {
		t.Errorf("the rows a new one lists are in %s and %s, not its folder", folderOf(a), folderOf(b))
	}

	d, changed, moved, err := plans.Move(ctx, a.ID, "/y")
	if err != nil || !changed || d.Folder != "/y" {
		t.Fatalf("moving a child: %+v, changed %v, %v", d, changed, err)
	}
	if !slices.Equal(moved, []string{"b", "big"}) || folderOf(parent) != "/y" || folderOf(b) != "/y" {
		t.Errorf("moving a child moved %q; the parent is in %s, the other child in %s", moved, folderOf(parent), folderOf(b))
	}
	if folderOf(other) != "/x" {
		t.Errorf("a row of no family moved too, to %s", folderOf(other))
	}
	if _, changed, moved, err := plans.Move(ctx, parent.ID, "/z"); err != nil || !changed || !slices.Equal(moved, []string{"a", "b"}) {
		t.Errorf("moving the parent: changed %v, moved %q, %v", changed, moved, err)
	}
	if _, changed, moved, err := plans.Move(ctx, b.ID, "z"); err != nil || changed || len(moved) != 0 {
		t.Errorf("moving to the family's folder: changed %v, moved %q, %v", changed, moved, err)
	}

	if _, err := plans.UpdateWithChildren(ctx, parent.ID, 1, []byte("2"), []string{a.ID, other.ID}); err != nil {
		t.Fatal(err)
	}
	if folderOf(other) != "/z" || folderOf(b) != "/z" {
		t.Errorf("after a new revision, a row now listed is in %s, one let go in %s; want both /z", folderOf(other), folderOf(b))
	}
	if _, _, moved, err := plans.Move(ctx, a.ID, "/w"); err != nil || !slices.Equal(moved, []string{"big", "other"}) || folderOf(b) != "/z" {
		t.Errorf("a row let go moved with its old family: moved %q, it is in %s", moved, folderOf(b))
	}

	// Rows from before folders may be apart; one already where the family goes is
	// not said to move.
	if _, err := f.db.Exec("UPDATE docs SET folder = '/v' WHERE id = ?", other.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, moved, err := plans.Move(ctx, a.ID, "/v"); err != nil || !slices.Equal(moved, []string{"big"}) {
		t.Errorf("moving to where one of the family is: moved %q, %v", moved, err)
	}
}
