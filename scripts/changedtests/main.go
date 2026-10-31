// Command changedtests prints the tests make test-changed repeats, a package a
// line: its directory, then the tests' names joined by |. They are those of the
// test files changed since a range's start, committed or not, that a change can
// reach: a changed test, or one that uses a changed helper, type or variable of
// its package. Comments and imports change no test.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: changedtests <range>")
		os.Exit(2)
	}
	packages, err := changedTests(".", os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "changedtests:", err)
		os.Exit(1)
	}
	for _, p := range packages {
		fmt.Println(p.dir, strings.Join(p.tests, "|"))
	}
}

// packageTests are the tests to repeat of the package in dir.
type packageTests struct {
	dir   string
	tests []string
}

func changedTests(repo, rng string) ([]packageTests, error) {
	start, _, _ := strings.Cut(rng, "..")
	files, err := changedFiles(repo, start)
	if err != nil {
		return nil, err
	}
	byDir := map[string][]string{}
	for _, f := range files {
		byDir[filepath.Dir(f)] = append(byDir[filepath.Dir(f)], f)
	}
	var packages []packageTests
	for dir, changed := range byDir {
		tests, err := testsOf(repo, dir, changed, start)
		if err != nil {
			return nil, err
		}
		if len(tests) > 0 {
			packages = append(packages, packageTests{dir: dir, tests: tests})
		}
	}
	slices.SortFunc(packages, func(a, b packageTests) int { return strings.Compare(a.dir, b.dir) })
	return packages, nil
}

// changedFiles lists the test files that differ from start, as they are now.
func changedFiles(repo, start string) ([]string, error) {
	var files []string
	for _, args := range [][]string{
		{"diff", "--name-only", "--diff-filter=d", start},
		{"ls-files", "--others", "--exclude-standard"},
	} {
		out, err := git(repo, args...)
		if err != nil {
			return nil, err
		}
		for f := range strings.Lines(out) {
			if f = strings.TrimSpace(f); strings.HasSuffix(f, "_test.go") {
				files = append(files, f)
			}
		}
	}
	slices.Sort(files)
	return slices.Compact(files), nil
}

// A declaration is a named thing of a package's test files. A method belongs to
// its type: what uses the type can reach it, called through an interface or not.
type declaration struct {
	file    string
	lines   [][2]int        // the rows of its parts, first and last
	uses    map[string]bool // every identifier in it
	test    bool
	setsUp  bool // TestMain or init: every test goes through it
	changed bool
}

// testsOf returns the tests of the changed files, of the package in dir, that a
// change since start can reach, sorted.
func testsOf(repo, dir string, changed []string, start string) ([]string, error) {
	declarations, err := parseDeclarations(repo, dir)
	if err != nil {
		return nil, err
	}
	for _, f := range changed {
		rows, err := changedRows(repo, f, start)
		if err != nil {
			return nil, err
		}
		for _, d := range declarations {
			if d.file == f && rows(d.lines) {
				d.changed = true
			}
		}
	}
	setUpChanged := false
	for _, d := range declarations {
		setUpChanged = setUpChanged || d.setsUp && d.changed
	}
	var tests []string
	for name, d := range declarations {
		if d.test && slices.Contains(changed, d.file) && (setUpChanged || reachesChange(declarations, name, map[string]bool{})) {
			tests = append(tests, name)
		}
	}
	slices.Sort(tests)
	return tests, nil
}

func reachesChange(declarations map[string]*declaration, name string, seen map[string]bool) bool {
	d, ok := declarations[name]
	if !ok || seen[name] {
		return false
	}
	seen[name] = true
	if d.changed {
		return true
	}
	for used := range d.uses {
		if reachesChange(declarations, used, seen) {
			return true
		}
	}
	return false
}

func parseDeclarations(repo, dir string) (map[string]*declaration, error) {
	paths, err := filepath.Glob(filepath.Join(repo, dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	declarations := map[string]*declaration{}
	fset := token.NewFileSet()
	for _, path := range paths {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		file := filepath.Join(dir, filepath.Base(path))
		add := func(name string, part ast.Node) *declaration {
			d := declarations[name]
			if d == nil {
				d = &declaration{file: file, uses: map[string]bool{}}
				declarations[name] = d
			}
			d.lines = append(d.lines, [2]int{fset.Position(part.Pos()).Line, fset.Position(part.End()).Line})
			ast.Inspect(part, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && id.Name != name {
					d.uses[id.Name] = true
				}
				return true
			})
			return d
		}
		for _, top := range f.Decls {
			switch top := top.(type) {
			case *ast.FuncDecl:
				if top.Recv != nil {
					add(receiverType(top), top)
					continue
				}
				d := add(top.Name.Name, top)
				d.test = isTest(top)
				d.setsUp = top.Name.Name == "TestMain" || top.Name.Name == "init"
			case *ast.GenDecl:
				for _, spec := range top.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						add(spec.Name.Name, spec)
					case *ast.ValueSpec:
						for _, name := range spec.Names {
							add(name.Name, spec)
						}
					}
				}
			}
		}
	}
	return declarations, nil
}

func receiverType(f *ast.FuncDecl) string {
	name := ""
	ast.Inspect(f.Recv.List[0].Type, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && name == "" {
			name = id.Name
		}
		return name == ""
	})
	return name
}

func isTest(f *ast.FuncDecl) bool {
	if !strings.HasPrefix(f.Name.Name, "Test") || f.Name.Name == "TestMain" || len(f.Type.Params.List) != 1 {
		return false
	}
	star, ok := f.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "T"
}

var hunk = regexp.MustCompile(`(?m)^@@ -\S+ \+(\d+)(?:,(\d+))? @@`)

// changedRows returns what tells whether a declaration's rows hold a change of
// file since start; a file git does not track is all changes.
func changedRows(repo, file, start string) (func(lines [][2]int) bool, error) {
	if _, err := git(repo, "ls-files", "--error-unmatch", file); err != nil {
		return func([][2]int) bool { return true }, nil
	}
	out, err := git(repo, "diff", "-U0", start, "--", file)
	if err != nil {
		return nil, err
	}
	type change struct{ first, count int }
	var changes []change
	for _, m := range hunk.FindAllStringSubmatch(out, -1) {
		first, _ := strconv.Atoi(m[1])
		count := 1
		if m[2] != "" {
			count, _ = strconv.Atoi(m[2])
		}
		changes = append(changes, change{first, count})
	}
	return func(lines [][2]int) bool {
		for _, part := range lines {
			for _, c := range changes {
				// A count of 0 is rows removed after first: within the part only
				// when it goes on past first.
				if c.count == 0 && c.first >= part[0] && c.first < part[1] {
					return true
				}
				if c.count > 0 && c.first <= part[1] && c.first+c.count > part[0] {
					return true
				}
			}
		}
		return false
	}, nil
}

func git(repo string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
}
