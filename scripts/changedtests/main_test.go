package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/runtest"
)

func TestMain(m *testing.M) {
	os.Exit(runtest.Main(m))
}

const base = `package a

import "testing"

type writer struct{}

func (writer) Write(p []byte) (int, error) {
	return len(p), nil
}

var limit = 1

func helper() int {
	return deeper()
}

func deeper() int {
	return 1
}

// TestPlain says little.
func TestPlain(t *testing.T) {
	t.Log("plain")
}

func TestHelper(t *testing.T) {
	helper()
}

func TestWriter(t *testing.T) {
	_ = writer{}
}

func TestLimit(t *testing.T) {
	_ = limit
}

func TestRecursive(t *testing.T) {
	recursive(0)
}

func recursive(n int) {
	recursive(n)
}
`

// other holds a test using a helper of another file, which is not repeated,
// as its file did not change.
const other = `package a

import "testing"

func TestElsewhere(t *testing.T) {
	helper()
}
`

func TestChangedTests(t *testing.T) {
	t.Parallel()
	all := []string{"TestHelper", "TestLimit", "TestPlain", "TestRecursive", "TestWriter"}
	tests := []struct {
		name string
		edit func(string) string
		want []string
	}{
		{"nothing", func(s string) string { return s }, nil},
		{"a comment", replace("// TestPlain says little.", "// TestPlain says less."), nil},
		{"an import", replace(`import "testing"`, "import (\n\t\"testing\"\n\t_ \"os\"\n)"), nil},
		{"a test", replace(`t.Log("plain")`, `t.Log("changed")`), []string{"TestPlain"}},
		{"a line removed from a test", replace("\tt.Log(\"plain\")\n", ""), []string{"TestPlain"}},
		{"a new test", appended("func TestNew(t *testing.T) {\n\tt.Log(\"new\")\n}\n"), []string{"TestNew"}},
		{"a helper a test calls", replace("return deeper()", "return deeper() + 1"), []string{"TestHelper"}},
		{"a helper the helper calls", replace("\treturn 1\n", "\treturn 2\n"), []string{"TestHelper"}},
		{"a method of a type a test uses", replace("return len(p), nil", "return 0, nil"), []string{"TestWriter"}},
		{"a variable", replace("var limit = 1", "var limit = 2"), []string{"TestLimit"}},
		{"a recursive helper", replace("\trecursive(n)\n", "\trecursive(n + 1)\n"), []string{"TestRecursive"}},
		{"init", appended("func init() {\n\tlimit = 3\n}\n"), all},
		{"TestMain", appended("func TestMain(m *testing.M) {\n\tm.Run()\n}\n"), all},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := gittest.Repo(t, "repo")
			write(t, repo, "pkg/a/a_test.go", base)
			write(t, repo, "pkg/a/other_test.go", other)
			commit(t, repo)
			write(t, repo, "pkg/a/a_test.go", tt.edit(base))

			var want []packageTests
			if tt.want != nil {
				want = []packageTests{{dir: "pkg/a", tests: tt.want}}
			}
			got, err := changedTests(repo, "HEAD..HEAD")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

func TestChangedTestsSeesCommitsAndNewFiles(t *testing.T) {
	t.Parallel()
	repo := gittest.Repo(t, "repo")
	write(t, repo, "pkg/a/a_test.go", base)
	write(t, repo, "pkg/b/b_test.go", "package b\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {\n\tt.Log(1)\n}\n\nfunc TestOtherB(t *testing.T) {}\n")
	commit(t, repo)
	gittest.Git(t, repo, "tag", "start")

	write(t, repo, "pkg/b/b_test.go", "package b\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {\n\tt.Log(2)\n}\n\nfunc TestOtherB(t *testing.T) {}\n")
	commit(t, repo)
	write(t, repo, "pkg/a/a_test.go", replace(`t.Log("plain")`, `t.Log("changed")`)(base))
	write(t, repo, "pkg/c/c_test.go", "package c\n\nimport \"testing\"\n\nfunc TestC(t *testing.T) {}\n")

	got, err := changedTests(repo, "start..HEAD")
	if err != nil {
		t.Fatal(err)
	}
	want := []packageTests{
		{dir: "pkg/a", tests: []string{"TestPlain"}},
		{dir: "pkg/b", tests: []string{"TestB"}},
		{dir: "pkg/c", tests: []string{"TestC"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestChangedTestsFileRemoved(t *testing.T) {
	t.Parallel()
	repo := gittest.Repo(t, "repo")
	write(t, repo, "pkg/a/a_test.go", base)
	commit(t, repo)
	gittest.Git(t, repo, "rm", "-q", "pkg/a/a_test.go")

	got, err := changedTests(repo, "HEAD..HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want nothing", got)
	}
}

func replace(old, with string) func(string) string {
	return func(s string) string {
		if !strings.Contains(s, old) {
			panic("not in the file: " + old)
		}
		return strings.Replace(s, old, with, 1)
	}
}

func appended(extra string) func(string) string {
	return func(s string) string { return s + "\n" + extra }
}

func write(t *testing.T, repo, file, content string) {
	t.Helper()
	path := filepath.Join(repo, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, repo string) {
	t.Helper()
	gittest.Git(t, repo, "add", ".")
	gittest.Git(t, repo, "commit", "-q", "-m", "change")
}
