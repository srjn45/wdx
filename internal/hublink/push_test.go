package hublink

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/hub/hubtest"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/waittest"
)

// memOutbox is an outbox kept in memory, as doc.Outbox keeps it in the
// database: a refused change holds up its doc's later ones.
type memOutbox struct {
	mu       sync.Mutex
	seq      int64
	rows     []hubapi.DocChange
	failed   map[int64]string
	enqueued int
	// all is what EnqueueAll queues anew.
	all     []hubapi.DocChange
	changed chan struct{}
}

func newMemOutbox() *memOutbox {
	return &memOutbox{failed: map[int64]string{}, changed: make(chan struct{})}
}

// notify wakes those waiting on the outbox; o.mu is held.
func (o *memOutbox) notify() {
	close(o.changed)
	o.changed = make(chan struct{})
}

func (o *memOutbox) add(changes ...hubapi.DocChange) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, c := range changes {
		o.seq++
		c.Seq = o.seq
		o.rows = append(o.rows, c)
	}
	o.notify()
}

func (o *memOutbox) Next(_ context.Context, limit int) ([]hubapi.DocChange, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	held := map[string]bool{}
	for _, c := range o.rows {
		if _, ok := o.failed[c.Seq]; ok {
			held[c.DocId] = true
		}
	}
	var list []hubapi.DocChange
	for _, c := range o.rows {
		if len(list) < limit && !held[c.DocId] {
			list = append(list, c)
		}
	}
	return list, nil
}

func (o *memOutbox) Done(_ context.Context, seqs []int64, _ time.Time) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.rows = slices.DeleteFunc(o.rows, func(c hubapi.DocChange) bool { return slices.Contains(seqs, c.Seq) })
	o.notify()
	return nil
}

func (o *memOutbox) Fail(_ context.Context, seq int64, code string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.failed[seq] = code
	o.notify()
	return nil
}

func (o *memOutbox) Clear(context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.rows, o.failed = nil, map[int64]string{}
	o.notify()
	return nil
}

func (o *memOutbox) EnqueueAll(context.Context) error {
	o.mu.Lock()
	o.enqueued++
	o.rows, o.failed = nil, map[int64]string{}
	o.mu.Unlock()
	o.add(o.all...)
	return nil
}

func (o *memOutbox) enqueuedTimes() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.enqueued
}

// state is the outbox's rows, each "<doc> <change>" with " failed:<code>"
// when refused.
func (o *memOutbox) state() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var list []string
	for _, c := range o.rows {
		s := c.DocId + " " + string(c.Change)
		if code, ok := o.failed[c.Seq]; ok {
			s += " failed:" + code
		}
		list = append(list, s)
	}
	return list
}

// wait waits for the outbox to be want.
func (o *memOutbox) wait(t *testing.T, want ...string) {
	t.Helper()
	timeout := waittest.After(t)
	for {
		o.mu.Lock()
		changed := o.changed
		o.mu.Unlock()
		got := o.state()
		if slices.Equal(got, want) {
			return
		}
		select {
		case <-changed:
		case <-timeout:
			t.Fatalf("outbox %q, want %q", got, want)
		}
	}
}

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

var pushTime = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// revision is the change of doc id's revision whose text is text, the doc
// named name.
func revision(t *testing.T, id, name string, rev int, text string) hubapi.DocChange {
	t.Helper()
	c := metadata(id, name)
	c.Change, c.Revision = hubapi.DocChangeTypeRevision, &rev
	content := gzipped(t, text)
	c.Content = &content
	return c
}

func metadata(id, name string) hubapi.DocChange {
	kind := hubapi.DocKindDoc
	return hubapi.DocChange{
		Change: hubapi.DocChangeTypeMetadata, DocId: id, RepoKey: ptr("github.com/o/app"), Kind: &kind, Name: &name,
		Folder: ptr(""), Format: ptr("md"), Labels: &[]string{}, Status: ptr("wip"), CreatedAt: &pushTime, UpdatedAt: &pushTime,
	}
}

func removed(id string) hubapi.DocChange {
	return hubapi.DocChange{Change: hubapi.DocChangeTypeRemoved, DocId: id}
}

func ptr[T any](v T) *T { return &v }

// pushNode is a node of h whose link pushes outbox, with opts changed by
// change unless nil.
func pushNode(t *testing.T, h *hubtest.Hub, outbox *memOutbox, change func(*Options)) *node {
	t.Helper()
	n := &node{t: t, h: h, home: t.TempDir()}
	n.log = n.home + "/hublink.log"
	n.opts = Options{MinBackoff: time.Millisecond, MaxBackoff: 10 * time.Millisecond, Outbox: outbox}
	if change != nil {
		change(&n.opts)
	}
	n.open()
	t.Cleanup(n.wantNoSecrets)
	return n
}

// hubContent is the text of the hub's doc id at rev.
func hubContent(t *testing.T, h *hubtest.Hub, id string, rev int) string {
	t.Helper()
	b, err := h.Server.Docs().Content(context.Background(), h.User.ID, id, rev)
	if err != nil {
		t.Fatalf("hub: doc %s revision %d: %v", id, rev, err)
	}
	return string(b)
}

func TestPush(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	outbox := newMemOutbox()
	n := pushNode(t, h, outbox, nil)
	text := "the text of the plan for the launch"
	n.secret(text)
	// What the login queues.
	outbox.all = []hubapi.DocChange{revision(t, "doc-a", "launch", 1, text)}
	for i := range 150 {
		outbox.all = append(outbox.all, revision(t, fmt.Sprintf("doc-%03d", i), fmt.Sprintf("n%03d", i), 1, "x"))
	}
	n.loggedIn("desktop")
	outbox.wait(t)

	if got := hubContent(t, h, "doc-a", 1); got != text {
		t.Fatalf("hub has %q", got)
	}
	var sent []float64
	for _, line := range n.logged("docs pushed") {
		sent = append(sent, line["sent"].(float64))
	}
	if !slices.Equal(sent, []float64{100, 51}) {
		t.Fatalf("batches of %v, want 100 then 51", sent)
	}

	// Each change from then on, kicked.
	outbox.add(revision(t, "doc-a", "launch", 2, "two"), metadata("doc-a", "launched"), removed("doc-000"))
	n.link.Kick()
	outbox.wait(t)
	d, err := h.Server.Docs().Doc(context.Background(), h.User.ID, "doc-a")
	if err != nil || d.Name != "launched" || d.Revision != 2 {
		t.Fatalf("hub: %+v %v", d, err)
	}
	if _, err := h.Server.Docs().Doc(context.Background(), h.User.ID, "doc-000"); err == nil {
		t.Fatal("hub: doc-000 not removed")
	}
}

// dropAnswer loses the hub's answer to the next push once armed: the hub
// applied it, and the node never hears.
type dropAnswer struct {
	next  http.RoundTripper
	armed atomic.Bool
}

func (d *dropAnswer) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := d.next.RoundTrip(req)
	if err != nil || req.URL.Path != "/v1/docs/changes" || !d.armed.CompareAndSwap(true, false) {
		return resp, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil, errors.New("the connection dropped")
}

func TestPushAnswerLost(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	outbox := newMemOutbox()
	drop := &dropAnswer{}
	n := pushNode(t, h, outbox, func(o *Options) {
		o.WrapNodes = func(rt http.RoundTripper) http.RoundTripper { drop.next = rt; return drop }
	})
	n.loggedIn("desktop")
	drop.armed.Store(true)
	outbox.add(revision(t, "doc-a", "a", 1, "one"), revision(t, "doc-a", "a", 2, "two"), metadata("doc-a", "b"))
	n.link.Kick()
	outbox.wait(t)

	if drop.armed.Load() {
		t.Fatal("no answer was lost")
	}
	if failed := n.logged("docs push failed"); len(failed) != 1 {
		t.Fatalf("pushes failed %d times, want once", len(failed))
	}
	stored := map[float64]int{}
	for _, line := range h.Logged("doc revision stored") {
		stored[line["revision"].(float64)]++
	}
	if stored[1] != 1 || stored[2] != 1 {
		t.Fatalf("revisions stored %v, want each once", stored)
	}
	if d, err := h.Server.Docs().Doc(context.Background(), h.User.ID, "doc-a"); err != nil || d.Revision != 2 || d.Name != "b" {
		t.Fatalf("hub: %+v %v", d, err)
	}
}

func TestPushRefused(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	outbox := newMemOutbox()
	n := pushNode(t, h, outbox, nil)
	n.loggedIn("desktop")
	outbox.add(revision(t, "doc-a", "notes", 1, "a"))
	n.link.Kick()
	outbox.wait(t)

	// Another node of the user made a doc of the name in the repository.
	outbox.add(revision(t, "doc-b", "notes", 1, "b"), revision(t, "doc-b", "notes", 2, "b2"), revision(t, "doc-c", "todo", 1, "c"))
	n.link.Kick()
	outbox.wait(t, "doc-b revision failed:doc_name_taken", "doc-b revision")
	if got := hubContent(t, h, "doc-c", 1); got != "c" {
		t.Fatalf("hub has %q", got)
	}
	lines := n.logged("doc push refused")
	if len(lines) != 1 || lines[0]["doc_id"] != "doc-b" || lines[0]["code"] != "doc_name_taken" || lines[0]["name"] != "notes" {
		t.Fatalf("refusals logged: %v", lines)
	}
}

// A doc whose id another user's doc has on the hub is not_found, which
// sending again never changes: it is refused for good, and the outbox goes
// on with the other docs.
func TestPushNotFound(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	bob := h.AddUser("bob")
	h.PushDoc(h.NewNode(bob, "laptop"), "doc-b", "github.com/o/app", "plans", "bob's")
	outbox := newMemOutbox()
	n := pushNode(t, h, outbox, nil)
	n.loggedIn("desktop")
	outbox.add(revision(t, "doc-b", "notes", 1, "b"), revision(t, "doc-c", "todo", 1, "c"))
	n.link.Kick()
	outbox.wait(t, "doc-b revision failed:not_found")
	if got := hubContent(t, h, "doc-c", 1); got != "c" {
		t.Fatalf("hub has %q", got)
	}
	if failed := n.logged("docs push failed"); len(failed) != 0 {
		t.Errorf("pushes failed %d times, want none: %v", len(failed), failed)
	}
}

func TestPushAtReconnect(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	outbox := newMemOutbox()
	n := pushNode(t, h, outbox, nil)
	n.loggedIn("desktop")
	h.Stop()
	n.waitConnection(Offline)
	outbox.add(revision(t, "doc-a", "offline", 1, "written offline"))
	n.link.Kick()
	h.Restart()
	n.waitConnection(Connected)
	outbox.wait(t)
	if got := hubContent(t, h, "doc-a", 1); got != "written offline" {
		t.Fatalf("hub has %q", got)
	}
}

func TestPushLogoutAndLogin(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	outbox := newMemOutbox()
	outbox.all = []hubapi.DocChange{revision(t, "doc-a", "a", 1, "one")}
	n := pushNode(t, h, outbox, nil)
	n.loggedIn("desktop")
	outbox.wait(t)
	if outbox.enqueuedTimes() != 1 {
		t.Fatal("not enqueued at the login")
	}

	h.Stop()
	n.waitConnection(Offline)
	outbox.add(revision(t, "doc-a", "a", 2, "two"))
	if _, err := n.link.Logout(n.ctx); err != nil {
		t.Fatal(err)
	}
	outbox.wait(t)

	h.Restart()
	outbox.all = append(outbox.all, revision(t, "doc-a", "a", 2, "two"))
	n.loggedIn("laptop")
	outbox.wait(t)
	if got := outbox.enqueuedTimes(); got != 2 {
		t.Fatalf("enqueued %d times, want at each login", got)
	}
	if got := hubContent(t, h, "doc-a", 2); got != "two" {
		t.Fatalf("hub has %q", got)
	}
}
