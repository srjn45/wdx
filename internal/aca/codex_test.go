package aca_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/runtest"
)

// codexAsksNothing is what every start passes, so that Codex starts at once in dir.
func codexAsksNothing(dir string) []string {
	return []string{"codex", "-c", "check_for_update_on_startup=false", "-c", `projects={"` + dir + `"={trust_level="trusted"}}`}
}

func TestByIDFindsCodex(t *testing.T) {
	t.Parallel()
	a, ok := aca.ByID("codex")
	if !ok || a.ID() != "codex" || a.Program() != "codex" {
		t.Fatalf("ByID(codex) = %v, %v", a, ok)
	}
	if aca.CanBeginSession(a) {
		t.Error("Codex can begin a session")
	}
	if _, ok := a.ScreenParser().(aca.CodexScreenParser); !ok {
		t.Errorf("Codex's parser is %T", a.ScreenParser())
	}
}

// The session id is not passed: Codex names its own.
func TestCodexStartsANewConversationAskingNothing(t *testing.T) {
	t.Parallel()
	got, err := aca.Codex{}.Command(aca.Launch{Dir: "/work", SessionID: "s-1", UserHome: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if want := codexAsksNothing("/work"); !slices.Equal(got, want) {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestCodexQuotesTheTrustedDir(t *testing.T) {
	t.Parallel()
	got, err := aca.Codex{}.Command(aca.Launch{Dir: `/a "b"\c`})
	if err != nil {
		t.Fatal(err)
	}
	if want := `projects={"/a \"b\"\\c"={trust_level="trusted"}}`; got[4] != want {
		t.Errorf("trust = %s, want %s", got[4], want)
	}
}

func TestCodexGivesTheModelPermissionsAndNote(t *testing.T) {
	t.Parallel()
	got, err := aca.Codex{}.Command(aca.Launch{Dir: "/work", Model: "gpt-6-luna", Permissions: aca.PermissionsFull, Note: "Run `wdx help agents`.\nSay \"done\"."})
	if err != nil {
		t.Fatal(err)
	}
	want := append(codexAsksNothing("/work"), "-m", "gpt-6-luna", "--dangerously-bypass-approvals-and-sandbox",
		"-c", `developer_instructions="Run `+"`wdx help agents`"+`.\nSay \"done\"."`)
	if !slices.Equal(got, want) {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestCodexAskPassesNoPermissions(t *testing.T) {
	t.Parallel()
	got, err := aca.Codex{}.Command(aca.Launch{Dir: "/work", Permissions: aca.PermissionsAsk})
	if err != nil {
		t.Fatal(err)
	}
	if want := codexAsksNothing("/work"); !slices.Equal(got, want) {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestCodexDeliver(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, message, want string }{
		{"plain, submitted by Tab", "hello", "\x1b[200~hello\x1b[201~\t"},
		{"lines stay content", "one\ntwo", "\x1b[200~one\ntwo\x1b[201~\t"},
		{"cannot end the paste", "a\x1b[201~b", "\x1b[200~ab\x1b[201~\t"},
		{"nor by a marker its removal makes", "a\x1b[20\x1b[201~1~b", "\x1b[200~ab\x1b[201~\t"},
		{"not read as a command", "/tmp is a dir", "\x1b[200~ /tmp is a dir\x1b[201~\t"},
		{"a mention as it is", "@main.go", "\x1b[200~@main.go\x1b[201~\t"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var got bytes.Buffer
			if err := (aca.Codex{}).Deliver(&got, c.message); err != nil {
				t.Fatal(err)
			}
			if got.String() != c.want {
				t.Fatalf("got %q, want %q", got.String(), c.want)
			}
		})
	}
}

func TestCodexDeliverFails(t *testing.T) {
	t.Parallel()
	for writes := range 2 {
		if err := (aca.Codex{}).Deliver(&failingWriter{writes: writes}, "hello"); err == nil {
			t.Fatalf("no error after %d writes", writes)
		}
	}
}

func TestCodexInterruptIsEsc(t *testing.T) {
	t.Parallel()
	if got := (aca.Codex{}).Interrupt(); string(got) != "\x1b" {
		t.Fatalf("got %q", got)
	}
}

// fakeCodexShell is a login shell that prints a banner, then answers `codex debug
// models` with the file models, and fails anything else.
func fakeCodexShell(t *testing.T, models string) string {
	t.Helper()
	shell := filepath.Join(t.TempDir(), "shell")
	script := "#!/bin/sh\necho 'welcome back'\ncase \"$2\" in\n'codex debug models') cat '" + models + "';;\n*) exit 1;;\nesac\n"
	if err := runtest.WriteExecutable(shell, script); err != nil {
		t.Fatal(err)
	}
	return shell
}

// The sample is codex 0.163.0's own output, with each model's long prompt cut.
func TestCodexModels(t *testing.T) {
	t.Parallel()
	sample, err := filepath.Abs("testdata/codex/0.163.0/debug_models.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := aca.Codex{}.Models(context.Background(), aca.Launch{Shell: fakeCodexShell(t, sample)})
	if err != nil {
		t.Fatal(err)
	}
	want := []aca.Model{
		{ID: "gpt-6.1-sol", Name: "GPT-6.1-Sol"},
		{ID: "gpt-6-astra", Name: "GPT-6-Astra"},
		{ID: "gpt-6.1-terra", Name: "GPT-6.1 Terra"},
		{ID: "gpt-6-sol", Name: "GPT-6-Sol"},
		{ID: "gpt-6-luna", Name: "GPT-6-Luna"},
		{ID: "gpt-5.6-sol", Name: "GPT-5.6-Sol"},
		{ID: "gpt-5.6-terra", Name: "GPT-5.6-Terra"},
		{ID: "gpt-5.6-luna", Name: "GPT-5.6-Luna"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("models = %v, want %v, the hidden ones left out", got, want)
	}
}

func TestCodexModelsFail(t *testing.T) {
	t.Parallel()
	garbage := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(garbage, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (aca.Codex{}).Models(context.Background(), aca.Launch{Shell: fakeCodexShell(t, garbage)}); err == nil || !strings.Contains(err.Error(), "codex debug models") {
		t.Errorf("unreadable output: %v", err)
	}
	shell := filepath.Join(t.TempDir(), "shell")
	if err := runtest.WriteExecutable(shell, "#!/bin/sh\nexit 1\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := (aca.Codex{}).Models(context.Background(), aca.Launch{Shell: shell}); err == nil {
		t.Error("a failing command is no error")
	}
}
