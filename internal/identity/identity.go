// Package identity holds the tool's name and build information.
//
// The tool is wdx, the successor of warden, and runs side by side with it.
// All values are overridable at build time via -ldflags.
package identity

import (
	"path/filepath"
	"strings"
)

// Set at build time via -ldflags "-X github.com/srjn45/wdx/internal/identity.<Var>=...".
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

// ConfigPath is the config file under home.
func ConfigPath(home string) string {
	return filepath.Join(home, "config.yaml")
}

// SocketPath is the Unix socket the daemon listens on under home.
func SocketPath(home string) string {
	return filepath.Join(home, Name+".sock")
}

// LockPath is the file the running daemon holds locked under home; it holds
// the daemon's pid.
func LockPath(home string) string {
	return filepath.Join(home, "daemon.lock")
}

// DBPath is the SQLite database file under home.
func DBPath(home string) string {
	return filepath.Join(home, Name+".db")
}

// ProjectsDir is where projects are created and cloned under home.
func ProjectsDir(home string) string {
	return filepath.Join(home, "projects")
}

// LogsDir is where the daily log files are written under home.
func LogsDir(home string) string {
	return filepath.Join(home, "logs")
}

// Where releases are published, apart from the old warden's in srjn45/warden;
// vars so a fork can build its own.
var (
	// ReleasesURL holds the releases, as <ReleasesURL>/v<version>/<archive>.
	ReleasesURL = "https://github.com/srjn45/wdx/releases/download"
	// ReleasesAPI lists the releases, newest first.
	ReleasesAPI = "https://api.github.com/repos/srjn45/wdx/releases"
)

// PreviousDir holds the version an update replaced, and the database as it
// was, until the next update or a rollback.
func PreviousDir(home string) string {
	return filepath.Join(home, "previous")
}

// UpdateLockPath is the file an update or rollback holds locked under home.
func UpdateLockPath(home string) string {
	return filepath.Join(home, "update.lock")
}
