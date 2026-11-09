package hubcli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/clikit/clikittest"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/logtest"
	"github.com/srjn45/wdx/internal/runtest"
)

// startup runs args against home as main does: on the settings that loaded
// when the command needs none or rewrites them, else failing as main fails.
func startup(t *testing.T, home string, args ...string) clikittest.Output {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := Env{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Getenv: homeVars(home), WorkDir: home}
	cfg, err := hubconfig.Load(homeVars(home))
	if err != nil && !RewritesSettings(args, err) && !NeedsNoSettings(args) {
		exit := ConfigError(args, env, err)
		return clikittest.Output{Exit: exit, Stdout: stdout.String(), Stderr: stderr.String()}
	}
	env.Config = cfg
	exit := Run(context.Background(), args, env)
	out := clikittest.Output{Exit: exit, Stdout: stdout.String(), Stderr: stderr.String()}
	logs := clikittest.Logged{Dir: hubconfig.LogsDir(home), Called: "hub called", Abandoned: "hub unreachable", Served: "request returned"}
	clikittest.WantLogged(t, logs, args, out, measureLinks)
	return out
}

// unsetHome is a home whose config file does not set public_url, which is
// required. It logs at debug, the level completions log at.
func unsetHome(t *testing.T) string {
	t.Helper()
	home := runtest.Dir(t)
	logtest.OnFailure(t, home)
	if err := os.WriteFile(hubconfig.ConfigPath(home), []byte("log:\n  level: debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := hubconfig.Load(homeVars(home)); err == nil {
		t.Fatal("want public_url required")
	}
	return home
}

func TestCompletionScript(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	out := hub(t, home, "completion", "zsh")
	if out.Exit != clikit.ExitOK || !strings.Contains(out.Stdout, "#compdef "+name) || !strings.Contains(out.Stdout, "__complete") {
		t.Fatalf("want the zsh script, got %+v", out)
	}
	v, exit := hubJSON(t, home, "completion", "fish")
	if exit != clikit.ExitOK || v["shell"] != "fish" || !strings.Contains(v["script"].(string), name) {
		t.Fatalf("want the fish script, got %d: %v", exit, v)
	}
}

func TestCompletionWithoutSettings(t *testing.T) {
	t.Parallel()
	home := unsetHome(t)
	out := startup(t, home, "__complete", "us")
	if out.Exit != clikit.ExitOK || !strings.Contains(out.Stdout, "user\t") {
		t.Fatalf("want user offered, got %+v", out)
	}
	// No hub answers: no usernames, and no failure.
	out = startup(t, home, "__complete", "user", "disable", "--username", "")
	if out.Exit != clikit.ExitOK || out.Stdout != ":4\n" {
		t.Fatalf("want nothing offered, got %+v", out)
	}
	out = startup(t, home, "completion", "bash")
	if out.Exit != clikit.ExitOK || !strings.Contains(out.Stdout, name) {
		t.Fatalf("want the bash script, got %+v", out)
	}
}

func TestHelpWithoutSettings(t *testing.T) {
	t.Parallel()
	home := unsetHome(t)
	out := startup(t, home, "--help")
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, name+": ") {
		t.Fatalf("want the help, got %+v", out)
	}
	out = startup(t, home, "help", "user", "list")
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, name+" user list: ") {
		t.Fatalf("want the help of user list, got %+v", out)
	}
}

func TestCommandWithoutSettings(t *testing.T) {
	t.Parallel()
	home := unsetHome(t)
	out := startup(t, home, "user", "list", "--json")
	var v map[string]any
	if err := json.Unmarshal([]byte(out.Stdout), &v); err != nil || out.Exit != clikit.ExitUsage || v["code"] != clikit.CodeUsage || !strings.Contains(v["detail"].(string), "public_url") {
		t.Fatalf("want public_url required, got %+v", out)
	}
}
