package server_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/hub/docsync"
	"github.com/srjn45/wdx/internal/hub/hubtest"
	"github.com/srjn45/wdx/internal/hubapi"
)

func gzipped(t *testing.T, text string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// docRevision is revision n of the doc id named name, its text sent to h
// as a secret: no line of the hub's log may hold it.
func docRevision(t *testing.T, h *hubtest.Hub, seq int64, id string, n int, name, text string) hubapi.DocChange {
	t.Helper()
	content := gzipped(t, text)
	h.Secret(text, base64.StdEncoding.EncodeToString(content))
	repo, kind, folder, format, status, when := "github.com/acme/app", hubapi.DocKindDoc, "/", "md", "wip", time.Now()
	labels := []string{"hub"}
	return hubapi.DocChange{
		Seq: seq, DocId: id, Change: hubapi.DocChangeTypeRevision, RepoKey: &repo, Kind: &kind, Name: &name, Folder: &folder, Format: &format,
		Labels: &labels, Status: &status, Revision: &n, CreatedAt: &when, UpdatedAt: &when, Content: &content,
	}
}

func push(t *testing.T, h *hubtest.Hub, n hubtest.Node, changes ...hubapi.DocChange) []hubapi.DocChangeResult {
	t.Helper()
	r, err := h.NodesClient(n).PushDocChangesWithResponse(context.Background(), hubapi.DocChanges{Changes: changes})
	if err != nil || r.JSON200 == nil {
		t.Fatalf("push: %v %d %s", err, r.StatusCode(), r.Body)
	}
	if len(r.JSON200.Results) != len(changes) {
		t.Fatalf("%d results for %d changes: %s", len(r.JSON200.Results), len(changes), r.Body)
	}
	return r.JSON200.Results
}

func wantResult(t *testing.T, what string, r hubapi.DocChangeResult, seq int64, code hubapi.ErrorCode) {
	t.Helper()
	switch {
	case r.Seq != seq:
		t.Fatalf("%s: result of seq %d, want %d", what, r.Seq, seq)
	case code == "" && (r.Status != hubapi.DocChangeResultStatusApplied || r.Code != nil):
		t.Fatalf("%s: %s %v, want applied", what, r.Status, r.Code)
	case code != "" && (r.Status != hubapi.DocChangeResultStatusRefused || r.Code == nil || *r.Code != code):
		t.Fatalf("%s: %s %v, want refused with %s", what, r.Status, r.Code, code)
	}
}

func TestDocChangesPushed(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	ctx := context.Background()
	desktop := h.NewNode(h.User, "desktop")
	laptop := h.NewNode(h.User, "laptop")
	tower := h.NewNode(h.AddUser("bob"), "tower")

	results := push(t, h, desktop,
		docRevision(t, h, 1, "doc-1", 1, "PLAN", "the first text"),
		docRevision(t, h, 2, "doc-1", 2, "PLAN", "the second text"),
		docRevision(t, h, 3, "doc-2", 2, "NOTES", "a gap"),
		docRevision(t, h, 4, "doc-3", 1, "TOO_BIG", strings.Repeat("a", docsync.MaxContent+1)),
	)
	wantResult(t, "revision 1", results[0], 1, "")
	wantResult(t, "revision 2", results[1], 2, "")
	wantResult(t, "a gap", results[2], 3, hubapi.ErrorCodeRevisionGap)
	wantResult(t, "too large", results[3], 4, hubapi.ErrorCodeDocTooLarge)
	got, err := h.Server.Docs().Content(ctx, h.User.ID, "doc-1", 2)
	if err != nil || string(got) != "the second text" {
		t.Fatalf("content %q, %v", got, err)
	}

	// Another node of the account makes a doc of the name in the repository.
	wantResult(t, "the name taken", push(t, h, laptop, docRevision(t, h, 1, "doc-4", 1, "plan", "another node's"))[0], 1, hubapi.ErrorCodeDocNameTaken)
	// Another node of the account wrote revision 2 of the doc.
	conflict := docRevision(t, h, 1, "doc-1", 2, "PLAN", "another second text")
	wantResult(t, "another revision 2", push(t, h, laptop, conflict)[0], 1, hubapi.ErrorCodeRevisionConflict)
	wrongHash := docRevision(t, h, 1, "doc-1", 3, "PLAN", "the third text")
	wrongHash.ContentSha256 = ptr(strings.Repeat("0", 64))
	wantResult(t, "a hash not of the content", push(t, h, laptop, wrongHash)[0], 1, hubapi.ErrorCodeInvalidArgument)
	// Another account's node changes nothing of alice's doc.
	removed := hubapi.DocChange{Seq: 1, DocId: "doc-1", Change: hubapi.DocChangeTypeRemoved}
	wantResult(t, "bob removes alice's doc", push(t, h, tower, removed)[0], 1, hubapi.ErrorCodeNotFound)
	if _, err := h.Server.Docs().Doc(ctx, h.User.ID, "doc-1"); err != nil {
		t.Fatalf("alice's doc after bob's removal: %v", err)
	}
	wantResult(t, "alice removes it", push(t, h, laptop, removed)[0], 1, "")
	if _, err := h.Server.Docs().Doc(ctx, h.User.ID, "doc-1"); !errors.Is(err, docsync.ErrNotFound) {
		t.Fatalf("alice's doc after her removal: %v", err)
	}
}

func TestDocChangesOverTheLimitRefusedWhole(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	desktop := h.NewNode(h.User, "desktop")
	first := docRevision(t, h, 1, "doc-1", 1, "PLAN", "a text")
	padding := strings.Repeat("A", 8<<20)
	h.Secret(padding[:64])
	body := `{"changes": [` + jsonOf(t, first) + `, {"seq": 2, "doc_id": "doc-2", "change": "revision", "content": "` + padding + `"}]}`
	r, err := h.NodesClient(desktop).PushDocChangesWithBodyWithResponse(context.Background(), "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, "a body over 8 MiB", r.StatusCode(), r.Body, http.StatusBadRequest, hubapi.ErrorCodeUsage)
	if _, err := h.Server.Docs().Doc(context.Background(), h.User.ID, "doc-1"); !errors.Is(err, docsync.ErrNotFound) {
		t.Fatalf("a change of the body refused applied: %v", err)
	}
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func ptr[T any](v T) *T { return &v }
