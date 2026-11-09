package hubcli

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hubapi"
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
