package daemon

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/hublink"
)

func TestGitMissing(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("open: %w", &git.MissingError{Err: exec.ErrNotFound})
	p := problemOf(context.Background(), err)
	if p.Code != api.ErrorCodeDependencyMissing || p.Status != 503 || p.Details == nil || (*p.Details)["dependency"] != "git" {
		t.Fatalf("got %+v", p)
	}
}

// A hub that refuses a node for its API version, either side, is
// hub_version_unsupported, naming the side to update.
func TestHubVersionRefused(t *testing.T) {
	t.Parallel()
	for code, update := range map[hubapi.ErrorCode]string{
		hubapi.ErrorCodeNodeVersionUnsupported: hubapi.BehindNode,
		hubapi.ErrorCodeHubVersionUnsupported:  hubapi.BehindHub,
	} {
		hp := hubapi.NewProblem(code, "x")
		hp.Details = &map[string]any{"update": update, "api_version": "0"}
		p := problemOf(context.Background(), hubFailure(&hublink.HubError{Problem: hp}))
		if p.Code != api.ErrorCodeHubVersionUnsupported || p.Details == nil || (*p.Details)["update"] != update {
			t.Errorf("%s: got %+v", code, p)
		}
	}
}

// The daemon casts remote's Code and hublink's ProblemCode to an
// api.ErrorCode, so each constant of them must be one: a code the CLI does
// not know is reported by its status alone.
func TestHubCodesAreAPICodes(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ dir, typ string }{{"../remote", "Code"}, {"../hublink", "ProblemCode"}} {
		codes := stringConsts(t, c.dir, c.typ)
		if len(codes) == 0 {
			t.Fatalf("no constants of %s in %s", c.typ, c.dir)
		}
		for name, code := range codes {
			if _, ok := api.Codes[api.ErrorCode(code)]; !ok {
				t.Errorf("%s: %s is %q, not an api.ErrorCode", c.dir, name, code)
			}
		}
	}
}

// stringConsts returns the string constants of type typ declared in the
// package in dir, by name.
func stringConsts(t *testing.T, dir, typ string) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	consts := map[string]string{}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != typ {
					continue
				}
				for i, name := range vs.Names {
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						t.Fatalf("%s: %s is not a string literal", path, name.Name)
					}
					v, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatal(err)
					}
					consts[name.Name] = v
				}
			}
		}
	}
	return consts
}
