// Package ghtest stands in for GitHub's CLI in tests, which never touch the network.
package ghtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/runtest"
)

// Fake is a gh that keeps one pull request per repository it is asked about, as git
// and sh can: a real program run the way wdx runs the real one.
type Fake struct {
	Program string
	dir     string
}

// New writes a Fake in dir, which must exist. `pr create` opens a pull request at the
// commit the checkout is on, numbered 12.
func New(t *testing.T, dir string) *Fake {
	t.Helper()
	f := &Fake{Program: filepath.Join(dir, "fake-gh"), dir: dir}
	script := fmt.Sprintf(`#!/bin/sh
state=%q
echo "$@" >> "$state/gh-calls"
if [ -f "$state/gh-refuses" ]; then cat "$state/gh-refuses" >&2; exit 1; fi
case "$1 $2" in
"pr view")
	if [ -f "$state/gh-pr" ]; then cat "$state/gh-pr"; exit 0; fi
	echo "no pull requests found for branch \"$3\"" >&2
	exit 1
	;;
"pr create")
	head=$(git rev-parse HEAD)
	printf '{"number":12,"url":"https://github.test/o/r/pull/12","state":"OPEN","headRefOid":"%%s"}' "$head" > "$state/gh-pr"
	echo "https://github.test/o/r/pull/12"
	;;
esac
`, dir)
	if err := runtest.WriteExecutable(f.Program, script); err != nil {
		t.Fatal(err)
	}
	return f
}

// Set makes the pull request be in state ("OPEN", "MERGED", "CLOSED"), at commit head.
func (f *Fake) Set(t *testing.T, state, head string) {
	t.Helper()
	body := fmt.Sprintf(`{"number":12,"url":"https://github.test/o/r/pull/12","state":%q,"headRefOid":%q}`, state, head)
	if err := os.WriteFile(filepath.Join(f.dir, "gh-pr"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// SetMerged makes the pull request merged, at commit head, its merge commit merge.
func (f *Fake) SetMerged(t *testing.T, head, merge string) {
	t.Helper()
	body := fmt.Sprintf(`{"number":12,"url":"https://github.test/o/r/pull/12","state":"MERGED","headRefOid":%q,"mergeCommit":{"oid":%q}}`, head, merge)
	if err := os.WriteFile(filepath.Join(f.dir, "gh-pr"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Refuse makes every later call fail, saying said.
func (f *Fake) Refuse(t *testing.T, said string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "gh-refuses"), []byte(said), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Calls returns what gh was asked, each call a line of its arguments.
func (f *Fake) Calls(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, "gh-calls"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// In is the Fake New wrote in dir.
func In(dir string) *Fake {
	return &Fake{Program: filepath.Join(dir, "fake-gh"), dir: dir}
}
