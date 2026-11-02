package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/srjn45/wdx/internal/daemon"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/runtest"
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
	if err := runtest.WriteExecutable(filepath.Join(dir, name), "#!/bin/sh\n"+script+"\n"); err != nil {
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
			if err := os.Symlink(path, filepath.Join(tools, tool)); err != nil {
				t.Fatal(err)
			}
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

// installRelease runs install.sh with the variables of env, as on macOS, from
// a release in a directory, for a new user whose home it returns.
func installRelease(t *testing.T, env ...string) string {
	t.Helper()
	home := runtest.Dir(t)
	wdxHome := filepath.Join(home, ".wdx")
	t.Cleanup(func() {
		if pid, _ := daemon.Running(wdxHome); pid != 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	// The daemon the installer starts must send no metrics.
	writeFile(t, identity.ConfigPath(wdxHome), "otel:\n  endpoint: \"\"\n")
	arch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[runtime.GOARCH]
	releases := t.TempDir()
	name := fmt.Sprintf("%s_%s_darwin_%s.tar.gz", identity.Name, releaseVersion, runtime.GOARCH)
	sum := placeArchive(t, filepath.Join(releases, "v"+releaseVersion, name), wdxBinary)
	writeFile(t, filepath.Join(releases, "v"+releaseVersion, "checksums.txt"), sum+"  "+name+"\n")
	stubs := t.TempDir()
	stub(t, stubs, "uname", `case "$1" in -s) echo Darwin ;; -m) echo `+arch+` ;; esac`)
	stub(t, stubs, "launchctl", "exit 0")

	cmd := exec.Command("sh", "../../scripts/install.sh")
	cmd.Env = append([]string{
		"HOME=" + home,
		"PATH=" + stubs + ":/usr/bin:/bin",
		"WDX_DOWNLOAD_BASE=file://" + releases,
		"WDX_VERSION=" + releaseVersion,
	}, env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	return home
}

// TestInstallLaunchd checks that the agent's plist lets the daemon stop in
// its own time.
func TestInstallLaunchd(t *testing.T) {
	t.Parallel()
	home := installRelease(t)
	plist := readFile(t, filepath.Join(home, "Library/LaunchAgents/com.github.srjn45.wdx.plist"))
	for _, want := range []string{"<key>AbandonProcessGroup</key>\n\t<true/>", "<key>ExitTimeOut</key>\n\t<integer>60</integer>"} {
		if !strings.Contains(plist, want) {
			t.Errorf("want %q in the plist, got\n%s", want, plist)
		}
	}
}

// TestInstallSavesChannel checks that wdx update goes on following the channel
// wdx was installed from.
func TestInstallSavesChannel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		env  []string
		want string
	}{
		{[]string{"WDX_CHANNEL=dev"}, "\n  channel: dev\n"},
		{nil, "\n#   channel: stable\n"},
		{[]string{"WDX_CHANNEL=stable"}, "\n#   channel: stable\n"},
	} {
		home := installRelease(t, tc.env...)
		if got := readFile(t, identity.ConfigPath(filepath.Join(home, ".wdx"))); !strings.Contains(got, tc.want) {
			t.Errorf("%v: want %q in the config file, got\n%s", tc.env, tc.want, got)
		}
	}
}

// TestInstallFindsNewestOnGitHub checks which release the installer goes for.
// curl is a stub that answers only where the newest is found, so the install
// ends at its download.
func TestInstallFindsNewestOnGitHub(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, channel, note, want string
	}{
		{"dev", "dev", "v0.3.0-dev.11", "downloading wdx v0.3.0-dev.11 "},
		{"stable", "stable", "v0.3.0-dev.11", "downloading wdx v0.2.0 "},
		{"dev with no file", "dev", "", "failed to resolve the latest dev release"},
	} {
		home := t.TempDir()
		stubs := t.TempDir()
		note := "exit 22"
		if tc.note != "" {
			note = "echo " + tc.note
		}
		stub(t, stubs, "curl", `case "$*" in
	*api.github.com/repos/srjn45/wdx/releases/latest*) echo '{"tag_name": "v0.2.0"}' ;;
	*raw.githubusercontent.com/srjn45/wdx/channels/LATEST-dev*) `+note+` ;;
	*) exit 22 ;;
esac`)
		cmd := exec.Command("sh", "../../scripts/install.sh")
		cmd.Env = []string{
			"HOME=" + home,
			"PATH=" + stubs + ":/usr/bin:/bin",
			"WDX_BIN_DIR=" + filepath.Join(home, "bin"),
			"WDX_CHANNEL=" + tc.channel,
		}
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("%s: want the install to stop before installing, got\n%s", tc.name, out)
		}
		if !strings.Contains(string(out), tc.want) {
			t.Errorf("%s: want %q, got\n%s", tc.name, tc.want, out)
		}
	}
}

// TestPublishLatest moves the dev channel's file through the releases of a
// repository served from a directory, and checks it only moves forward.
func TestPublishLatest(t *testing.T) {
	t.Parallel()
	script, err := filepath.Abs("../../scripts/publish-latest.sh")
	if err != nil {
		t.Fatal(err)
	}
	origin := t.TempDir()
	clone := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(origin, "init", "-q", "--bare")
	git(clone, "init", "-q")
	git(clone, "remote", "add", "origin", origin)
	publishLatest := func(tag string) ([]byte, error) {
		cmd := exec.Command("sh", script, tag)
		cmd.Dir = clone
		return cmd.CombinedOutput()
	}

	commits := 0
	for _, tc := range []struct {
		tag, want string
		moves     bool
	}{
		{"v0.3.0-dev.9", "v0.3.0-dev.9", true}, // the first makes the branch
		{"v0.3.0-dev.11", "v0.3.0-dev.11", true},
		{"v0.3.0-dev.11", "v0.3.0-dev.11", false}, // a run again
		{"v0.3.0-dev.4", "v0.3.0-dev.11", false},  // an old tag again
		{"v0.2.0", "v0.3.0-dev.11", false},
		{"v0.3.0", "v0.3.0", true}, // stable comes after its dev releases
		{"v0.3.0-dev.12", "v0.3.0", false},
		{"v0.4.0-dev.1", "v0.4.0-dev.1", true},
	} {
		if out, err := publishLatest(tc.tag); err != nil {
			t.Fatalf("%s: %v\n%s", tc.tag, err, out)
		}
		if tc.moves {
			commits++
		}
		if got := git(origin, "show", "channels:LATEST-dev"); got != tc.want {
			t.Errorf("after %s: file names %q, want %q", tc.tag, got, tc.want)
		}
		if got := git(origin, "rev-list", "--count", "channels"); got != fmt.Sprint(commits) {
			t.Errorf("after %s: %s commits, want %d", tc.tag, got, commits)
		}
	}

	if out, err := publishLatest("nightly"); err == nil {
		t.Errorf("want a tag that is no release refused, got\n%s", out)
	}
	if got := git(origin, "show", "channels:LATEST-dev"); got != "v0.4.0-dev.1" {
		t.Errorf("a refused tag moved the file to %q", got)
	}
}
