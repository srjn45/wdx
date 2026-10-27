// Package identity holds the tool's name and build information.
//
// During the rebuild the tool ships as "wdx" so it can run side by side with
// the old warden. All values are overridable at build time via -ldflags.
package identity

import (
	"os"
	"path/filepath"
	"strings"
)

// Set at build time via -ldflags "-X github.com/srjn45/warden/internal/identity.<Var>=...".
var (
	// Name is the command name.
	Name = "wdx"
	// Version is the release version; "dev" for local builds.
	Version = "dev"
	// Commit is the git commit the binary was built from.
	Commit = "none"
)

// Env returns the environment variable name for key, e.g. Env("HOME") == "WDX_HOME".
func Env(key string) string {
	return strings.ToUpper(strings.ReplaceAll(Name, "-", "_")) + "_" + key
}

// HomeDir is the root of all state: $WDX_HOME if set, else ~/.wdx.
func HomeDir(getenv func(string) string) string {
	if h := getenv(Env("HOME")); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, "."+Name)
}

// DBPath is the SQLite database file under home.
func DBPath(home string) string {
	return filepath.Join(home, Name+".db")
}

// ProjectsDir is where projects are created and cloned under home.
func ProjectsDir(home string) string {
	return filepath.Join(home, "projects")
}
