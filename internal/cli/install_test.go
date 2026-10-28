package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// installer runs scripts/install.sh with args for a user whose home is home
// and who has the shells of stubs, which stands first in PATH.
func installer(t *testing.T, home, stubs string, args ...string) string {
	t.Helper()
	cmd := exec.Command("sh", append([]string{"../../scripts/install.sh"}, args...)...)
	cmd.Env = []string{
		"HOME=" + home,
		"WDX_BIN_DIR=" + filepath.Join(home, "bin"),
		"PATH=" + stubs + ":/usr/bin:/bin",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// stub writes an executable named name to dir that runs script.
func stub(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// installedHome returns a home with wdx installed in its bin, and a
// directory of stubs for zsh, fish and bash.
func installedHome(t *testing.T) (home, stubs string) {
	t.Helper()
	home, stubs = t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(wdxBinary, filepath.Join(home, "bin", "wdx")); err != nil {
		t.Fatal(err)
	}
	// The installer settles which shells exist with command -v, and reads
	// zsh's fpath from zsh itself.
	stub(t, stubs, "bash", "exit 0")
	stub(t, stubs, "fish", "exit 0")
	stub(t, stubs, "zsh", "echo /usr/share/zsh/functions")
	return home, stubs
}

func TestInstallCompletions(t *testing.T) {
	t.Parallel()
	home, stubs := installedHome(t)
	out := installer(t, home, stubs, "--completions")
	files := map[string]string{
		".local/share/bash-completion/completions/wdx": "__wdx_get_completion_results",
		".zsh/completions/_wdx":                        "#compdef wdx",
		".config/fish/completions/wdx.fish":            "__wdx_perform_completion",
	}
	for path, want := range files {
		b, err := os.ReadFile(filepath.Join(home, path))
		if err != nil || !strings.Contains(string(b), want) {
			t.Errorf("%s: want a script containing %q, got %v", path, want, err)
		}
	}
	if !strings.Contains(out, "fpath=("+filepath.Join(home, ".zsh/completions")+" $fpath)") {
		t.Errorf("want the line zsh needs, got:\n%s", out)
	}

	// Once ~/.zshrc adds the directory, nothing is asked of the user.
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte("fpath=(~/.zsh/completions $fpath)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := installer(t, home, stubs, "--completions"); strings.Contains(out, "fpath=(") {
		t.Errorf("want no zsh hint, got:\n%s", out)
	}

	out = installer(t, home, stubs, "--uninstall")
	for path := range files {
		if _, err := os.Stat(filepath.Join(home, path)); !os.IsNotExist(err) {
			t.Errorf("%s: want it removed, got %v\n%s", path, err, out)
		}
	}
}

func TestInstallCompletionsHomebrew(t *testing.T) {
	t.Parallel()
	home, stubs := installedHome(t)
	prefix := t.TempDir()
	siteFunctions := filepath.Join(prefix, "share/zsh/site-functions")
	if err := os.MkdirAll(siteFunctions, 0o755); err != nil {
		t.Fatal(err)
	}
	stub(t, stubs, "brew", "echo "+prefix)
	// Homebrew's zsh searches its site-functions.
	stub(t, stubs, "zsh", "echo "+siteFunctions)
	out := installer(t, home, stubs, "--completions")
	if _, err := os.Stat(filepath.Join(siteFunctions, "_wdx")); err != nil || strings.Contains(out, "fpath=(") {
		t.Fatalf("want _wdx in Homebrew's site-functions and no hint, got %v:\n%s", err, out)
	}
	installer(t, home, stubs, "--uninstall")
	if _, err := os.Stat(filepath.Join(siteFunctions, "_wdx")); !os.IsNotExist(err) {
		t.Fatalf("want it removed, got %v", err)
	}
}

func TestInstallCompletionsShellsMissing(t *testing.T) {
	t.Parallel()
	home, stubs := installedHome(t)
	os.Remove(filepath.Join(stubs, "zsh"))
	os.Remove(filepath.Join(stubs, "fish"))
	// /usr/bin may hold the real ones: run with only the stubs and what sh
	// needs.
	tools := t.TempDir()
	for _, tool := range []string{"sh", "uname", "tr", "dirname", "mkdir", "mv", "rm", "env", "grep", "printf", "id"} {
		if path, err := exec.LookPath(tool); err == nil {
			os.Symlink(path, filepath.Join(tools, tool))
		}
	}
	cmd := exec.Command("sh", "../../scripts/install.sh", "--completions")
	cmd.Env = []string{"HOME=" + home, "WDX_BIN_DIR=" + filepath.Join(home, "bin"), "PATH=" + stubs + ":" + tools}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for path, want := range map[string]bool{
		".local/share/bash-completion/completions/wdx": true,
		".zsh/completions/_wdx":                        false,
		".config/fish/completions/wdx.fish":            false,
	} {
		if _, err := os.Stat(filepath.Join(home, path)); (err == nil) != want {
			t.Errorf("%s: want installed %v, got %v", path, want, err)
		}
	}
}
