// Package gittest makes the git binary behave the same for every test run,
// whoever runs it and wherever.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Isolate sets up the environment of the test process, before any test
// runs, so git ignores the user's and system config, never asks for
// credentials and commits as a fixed author. It also clears inherited GIT_*
// variables, which git hooks set and which would point git at another repo.
func Isolate() {
	for _, kv := range os.Environ() {
		if key, _, _ := strings.Cut(kv, "="); strings.HasPrefix(key, "GIT_") {
			os.Unsetenv(key)
		}
	}
	for key, value := range map[string]string{
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_AUTHOR_NAME":     "test",
		"GIT_AUTHOR_EMAIL":    "test@example.com",
		"GIT_COMMITTER_NAME":  "test",
		"GIT_COMMITTER_EMAIL": "test@example.com",
		// Not covered by GIT_CONFIG_GLOBAL: these default to files under ~/.config/git.
		"GIT_CONFIG_COUNT":   "2",
		"GIT_CONFIG_KEY_0":   "core.excludesFile",
		"GIT_CONFIG_VALUE_0": os.DevNull,
		"GIT_CONFIG_KEY_1":   "core.attributesFile",
		"GIT_CONFIG_VALUE_1": os.DevNull,
	} {
		os.Setenv(key, value)
	}
}

// Git runs git in dir, failing the test when it fails.
func Git(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// Repo creates a repository named name with one empty commit, in a
// temporary directory with symlinks resolved.
func Repo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(tempDir(t), name)
	Git(t, filepath.Dir(dir), "init", "-q", dir)
	Git(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

// Bare creates a bare repository with one commit, to use as a remote.
func Bare(t *testing.T, name string) string {
	t.Helper()
	bare := filepath.Join(tempDir(t), name+".git")
	Git(t, Repo(t, name), "clone", "-q", "--bare", ".", bare)
	return bare
}

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
