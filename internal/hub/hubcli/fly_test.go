package hubcli

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/runtest"
)

// TestFlyConfig checks that deploy/hub/fly.toml serves the ports the hub
// listens on by default, mounts its volume where the Dockerfile puts the
// hub's home and checks the hub's health on its version route, so that a
// changed default fails here rather than at a deploy.
func TestFlyConfig(t *testing.T) {
	t.Parallel()
	fly := tomlSections(t, readTestFile(t, "../../../deploy/hub/fly.toml"))
	defaults := hubconfig.Default()
	version, err := hubapi.NewGetVersionRequest("http://hub")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ section, key, want, from string }{
		{"http_service", "internal_port", port(t, defaults.ListenWeb), "listen.web's default"},
		{"services", "internal_port", port(t, defaults.ListenNodes), "listen.nodes' default"},
		{"mounts", "destination", dockerfileEnv(t, "WDX_HUB_HOME"), "the Dockerfile's WDX_HUB_HOME"},
		{"http_service.checks", "path", version.URL.Path, "the web endpoint's version route"},
	} {
		if got := fly[c.section][c.key]; got != c.want {
			t.Errorf("fly.toml's [%s] %s is %q, want %s, %s", c.section, c.key, got, c.want, c.from)
		}
	}
}

// tomlSections reads the key = value lines of each table of a TOML file, by
// the table's name, strings unquoted; of an array of tables ([[name]]), its
// first entry's.
// It knows only the TOML fly.toml is written in, and no library is needed.
func tomlSections(t *testing.T, content string) map[string]map[string]string {
	t.Helper()
	sections := map[string]map[string]string{}
	var current map[string]string
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "["):
			name := strings.Trim(line, "[]")
			if _, seen := sections[name]; seen {
				current = nil
				continue
			}
			current = map[string]string{}
			sections[name] = current
		case current != nil:
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				t.Fatalf("fly.toml: a line that is no key = value: %q", line)
			}
			value = strings.TrimSpace(value)
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			}
			current[strings.TrimSpace(key)] = value
		}
	}
	return sections
}

func port(t *testing.T, address string) string {
	t.Helper()
	_, p, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// dockerfileEnv is the value deploy/hub/Dockerfile's ENV gives a variable.
func dockerfileEnv(t *testing.T, name string) string {
	t.Helper()
	for line := range strings.SplitSeq(readTestFile(t, "../../../deploy/hub/Dockerfile"), "\n") {
		if value, ok := strings.CutPrefix(line, "ENV "+name+"="); ok {
			return value
		}
	}
	t.Fatalf("deploy/hub/Dockerfile sets no %s", name)
	return ""
}

// TestFlyDeployRegion checks that scripts/hub-fly-deploy.sh puts an app's
// first machine in the region asked for, and leaves a machine already there
// where it is. It runs the script on a tagged checkout of its own with a
// flyctl that only says what it was asked and fails the deploy, so that
// nothing reaches Fly or the network.
func TestFlyDeployRegion(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name          string
		machine       bool
		region        string
		wantOK        bool // the script reaches fly deploy
		wantPrimary   bool // with --primary-region <region>
		wantInOutput  string
		wantNotOutput string
	}{
		{name: "first deploy without a region", wantInOutput: "make hub-fly-deploy APP=hub REGION=<region>"},
		{name: "first deploy", region: "fra", wantOK: true, wantPrimary: true},
		{name: "machine and a region", machine: true, region: "fra", wantOK: true, wantInOutput: "REGION=fra ignored"},
		{name: "machine", machine: true, wantOK: true, wantNotOutput: "ignored"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out, calls := runFlyDeploy(t, c.machine, c.region)
			deploy := ""
			for _, call := range calls {
				if strings.HasPrefix(call, "deploy ") {
					deploy = call
				}
			}
			if got := deploy != ""; got != c.wantOK {
				t.Fatalf("fly deploy run: %v, want %v; flyctl was asked %q\n%s", got, c.wantOK, calls, out)
			}
			if got := strings.Contains(deploy, "--primary-region "+c.region); c.wantOK && got != c.wantPrimary {
				t.Errorf("fly deploy with --primary-region %s: %v, want %v: %s", c.region, got, c.wantPrimary, deploy)
			}
			if c.wantOK && !c.wantPrimary && strings.Contains(deploy, "--primary-region") {
				t.Errorf("fly deploy names a region for a machine already placed: %s", deploy)
			}
			if !strings.Contains(out, c.wantInOutput) {
				t.Errorf("the output lacks %q:\n%s", c.wantInOutput, out)
			}
			if c.wantNotOutput != "" && strings.Contains(out, c.wantNotOutput) {
				t.Errorf("the output has %q:\n%s", c.wantNotOutput, out)
			}
		})
	}
}

// runFlyDeploy runs scripts/hub-fly-deploy.sh for the app hub, which has a
// machine or none, with REGION set to region unless it is empty. It returns
// the script's output and the arguments of each flyctl it ran.
func runFlyDeploy(t *testing.T, machine bool, region string) (string, []string) {
	t.Helper()
	repo := gittest.Repo(t, "wdx")
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "hub-fly-deploy.sh"), []byte(readTestFile(t, "../../../scripts/hub-fly-deploy.sh")), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, repo, "add", ".")
	gittest.Git(t, repo, "commit", "-q", "-m", "the script")
	gittest.Git(t, repo, "tag", "v1.2.3")

	stubs := t.TempDir()
	calls := filepath.Join(stubs, "calls")
	machines := "[]"
	if machine {
		machines = `[{"id": "m1"}]`
	}
	flyctl := `printf '%s\n' "$*" >> ` + calls + `
case "$1 $2" in
"machines list") echo '` + machines + `' ;;
"volumes list") echo '[{"id": "vol_1"}]' ;;
"volumes snapshots") echo '{"id": "vs_1"}' ;;
*) exit 1 ;;
esac`
	if err := runtest.WriteExecutable(filepath.Join(stubs, "flyctl"), "#!/bin/sh\n"+flyctl+"\n"); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sh", filepath.Join(repo, "scripts", "hub-fly-deploy.sh"))
	cmd.Env = append(os.Environ(), "PATH="+stubs+":/usr/bin:/bin", "APP=hub", "REGION="+region)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the script succeeded with a deploy that fails:\n%s", out)
	}
	logged, err := os.ReadFile(calls)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(out), strings.Split(strings.TrimSpace(string(logged)), "\n")
}
