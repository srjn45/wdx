package tidestate

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// TestView: tide's view as a whole and each project's are kept apart, {} until
// one is saved, replaced whole, and a project's goes with it.
func TestView(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p, other := f.project(t, "wdx"), f.project(t, "other")
	want := func(what string, got json.RawMessage, err error, view string) {
		t.Helper()
		if err != nil || string(got) != view {
			t.Errorf("%s: %s, %v; want %s", what, got, err, view)
		}
	}
	got, err := f.store.View(ctx)
	want("none saved", got, err, `{}`)
	got, err = f.store.ProjectView(ctx, p)
	want("no project's saved", got, err, `{}`)

	for _, v := range []string{`{"focus":"agent"}`, `{"focus":"project","zoomed":"agent"}`} {
		if err := f.store.ReplaceView(ctx, json.RawMessage(v)); err != nil {
			t.Fatal(err)
		}
		got, err = f.store.View(ctx)
		want("saved", got, err, v)
	}
	if err := f.store.ReplaceProjectView(ctx, p, json.RawMessage(`{"tab":"settings"}`)); err != nil {
		t.Fatal(err)
	}
	if err := f.store.ReplaceProjectView(ctx, other, json.RawMessage(`{"tab":"agent-x"}`)); err != nil {
		t.Fatal(err)
	}
	got, err = f.store.ProjectView(ctx, p)
	want("the project's", got, err, `{"tab":"settings"}`)

	for _, v := range []string{`[]`, `"x"`, `null`, `{`, ``} {
		if err := f.store.ReplaceProjectView(ctx, p, json.RawMessage(v)); !errors.Is(err, ErrNotObject) {
			t.Errorf("%q: %v", v, err)
		}
		if err := f.store.ReplaceView(ctx, json.RawMessage(v)); !errors.Is(err, ErrNotObject) {
			t.Errorf("%q as a whole: %v", v, err)
		}
	}

	f.exec(t, "DELETE FROM projects WHERE id = ?", p)
	got, err = f.store.ProjectView(ctx, p)
	want("a removed project's", got, err, `{}`)
	got, err = f.store.ProjectView(ctx, other)
	want("the other project's", got, err, `{"tab":"agent-x"}`)
}
