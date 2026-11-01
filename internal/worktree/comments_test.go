package worktree

import (
	"strings"
	"testing"
)

func TestReviewMessage(t *testing.T) {
	t.Parallel()
	got := ReviewMessage("auth-fix", " look at the nil checks ", []Comment{
		{File: "b.go", Line: 7, Side: SideNew, View: ViewCommitted, Quote: "second", Body: "later file"},
		{File: "a.go", Line: 42, Side: SideOld, View: ViewUncommitted, Quote: "if x {", Body: "keep it"},
		{File: "a.go", Line: 3, Side: SideNew, View: ViewUncommitted, Body: "no quote"},
	})
	want := `Review of auth-fix, 3 comments. Please address them.

look at the nil checks

a.go
  line 3 (uncommitted):
  no quote
  removed line 42 (uncommitted):
  > if x {
  keep it

b.go
  line 7 (committed):
  > second
  later file
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if one := ReviewMessage("w", "", []Comment{{File: "f", Line: 1, Body: "x"}}); !strings.HasPrefix(one, "Review of w, 1 comment. Please") {
		t.Errorf("one: %s", one)
	}
}
