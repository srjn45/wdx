package doc

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// plans returns a store of plans on the fixture's database, keeping its copies
// apart from the docs'.
func (f fixture) plans(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "plans")
	return NewKindStore(f.db, dir, KindPlan), dir
}

// TestKindsKeptApart: a doc and a plan can share a name, and no request of one
// store reaches the other's.
func TestKindsKeptApart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	plans, plansDir := f.plans(t)
	p := f.project(t, "wdx")
	d := f.create(t, p, New{Name: "auth", Format: "md", Content: []byte("a doc")})
	plan, err := plans.Create(ctx, p, New{Name: "auth", Format: "yaml", Content: []byte("goal: x")})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Name != "auth" {
		t.Errorf("a plan's name shown as %q", plan.Name)
	}
	var stored string
	if err := f.db.QueryRow("SELECT name FROM docs WHERE id = ?", plan.ID).Scan(&stored); err != nil || stored != "plan:auth" {
		t.Errorf("a plan's name stored as %q, %v", stored, err)
	}

	for _, c := range []struct {
		name  string
		store *Store
		own   Doc
		other Doc
	}{{"docs", f.store, d, plan}, {"plans", plans, plan, d}} {
		if got, err := c.store.GetByName(ctx, p, "AUTH"); err != nil || got.ID != c.own.ID {
			t.Errorf("%s: by name: %+v, %v", c.name, got, err)
		}
		if list, err := c.store.List(ctx, p, nil, ""); err != nil || len(list) != 1 || list[0].ID != c.own.ID {
			t.Errorf("%s: list: %+v, %v", c.name, list, err)
		}
		reached := map[string]error{}
		_, reached["get"] = c.store.Get(ctx, c.other.ID)
		_, _, reached["content"] = c.store.Content(ctx, c.other.ID, 0)
		_, reached["revisions"] = c.store.Revisions(ctx, c.other.ID)
		_, reached["update"] = c.store.Update(ctx, c.other.ID, 1, []byte("changed"))
		_, _, reached["set"] = c.store.Set(ctx, c.other.ID, Change{Status: StatusReview})
		_, reached["rename"] = c.store.Rename(ctx, c.other.ID, "renamed")
		_, reached["remove"] = c.store.Remove(ctx, c.other.ID)
		for what, err := range reached {
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("%s: %s of the other kind's: %v, want not found", c.name, what, err)
			}
		}
	}
	if got := f.content(t, d.ID, 0); got != "a doc" {
		t.Errorf("the doc changed: %q", got)
	}

	if err := plans.CatchUpCopies(ctx); err != nil {
		t.Fatal(err)
	}
	if !current(filepath.Join(plansDir, "wdx", "auth.yaml"), plan.UpdatedAt) {
		t.Error("no current copy of the plan in the plans' directory")
	}
	if current(filepath.Join(plansDir, "wdx", "auth.md"), d.UpdatedAt) {
		t.Error("the doc was copied among the plans")
	}
}

func TestPlanNames(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	plans, _ := f.plans(t)
	p := f.project(t, "wdx")
	var invalid *InvalidNameError
	for _, name := range []string{"auth_v2", strings.Repeat("a", 96), "", "plan:auth"} {
		if _, err := plans.Create(ctx, p, New{Name: name}); !errors.As(err, &invalid) {
			t.Errorf("plan %q: %v, want an invalid name", name, err)
		}
	}
	if _, err := plans.Create(ctx, p, New{Name: strings.Repeat("a", 95)}); err != nil {
		t.Errorf("95 characters: %v", err)
	}
	if _, err := f.store.Create(ctx, p, New{Name: "auth_v2"}); err != nil {
		t.Errorf("a doc keeps its _: %v", err)
	}

	plan, err := plans.Create(ctx, p, New{Name: "sso"})
	if err != nil {
		t.Fatal(err)
	}
	var taken *NameTakenError
	if _, err := plans.Create(ctx, p, New{Name: "SSO"}); !errors.As(err, &taken) || taken.Doc.ID != plan.ID || taken.Doc.Name != "sso" {
		t.Errorf("another plan's name: %v", err)
	}
	if _, err := plans.Rename(ctx, plan.ID, "auth_v2"); !errors.As(err, &invalid) {
		t.Errorf("renamed with an _: %v", err)
	}
	f.create(t, p, New{Name: "login"})
	if got, err := plans.Rename(ctx, plan.ID, "login"); err != nil || got.Name != "login" {
		t.Errorf("renamed to a doc's name: %+v, %v", got, err)
	}
}

func TestSyncToGitDocsOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	plans, _ := f.plans(t)
	p := f.project(t, "wdx")
	root := t.TempDir()
	if _, err := plans.SyncToGit(context.Background(), p, root, "docs"); err == nil {
		t.Error("plans synced to git")
	}
}
