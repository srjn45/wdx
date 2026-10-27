// Package gittest makes the git binary behave the same for every test run,
// whoever runs it and wherever.
package gittest

import (
	"os"
	"strings"
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
