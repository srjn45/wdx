package daemon

import (
	"testing"

	"github.com/srjn45/wdx/internal/doc"
)

// A revision goes to the hub with the sha256 of its text, which the hub keeps
// to tell another text under the revision from the same one sent again.
func TestDocChangeSendsHash(t *testing.T) {
	t.Parallel()
	sum := "a86abf6b98cfe6014875f1e4f0d435f0cde72673653a991e292660d7d779e269"
	h := docChange(doc.Outgoing{Seq: 1, Change: doc.ChangeRevision, DocID: "doc-1", Kind: doc.KindDoc, Revision: 1, Content: []byte("gz"), ContentSHA256: sum})
	if h.ContentSha256 == nil || *h.ContentSha256 != sum {
		t.Fatalf("content_sha256 %v, want %s", h.ContentSha256, sum)
	}
	if m := docChange(doc.Outgoing{Seq: 2, Change: doc.ChangeMetadata, DocID: "doc-1", Kind: doc.KindDoc}); m.ContentSha256 != nil {
		t.Fatalf("a metadata change has content_sha256 %q", *m.ContentSha256)
	}
}
