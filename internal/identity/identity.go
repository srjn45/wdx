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

// ConfigLockPath is the file held locked while the config file is rewritten.
func ConfigLockPath(home string) string {
	return filepath.Join(home, "config.lock")
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

// HolderSocket is the Unix socket the holder of terminal id listens on under
// home. Its directory is the holders' alone: only the user may enter it.
func HolderSocket(home, id string) string {
	return filepath.Join(home, "run", id+".sock")
}

// CodesignDir holds, on macOS, the keychain wdx signs itself with and the
// keychain's password (the wdx doc MAC_SIGNING). Only the user may enter it.
func CodesignDir(home string) string {
	return filepath.Join(home, "codesign")
}

// DocsDir holds a read-only copy of each doc's latest revision under home, to
// recover docs from if the database is lost (docs/DOCS.md).
func DocsDir(home string) string {
	return filepath.Join(home, "docs")
}

// PlansDir holds a read-only copy of each plan's latest revision under home, as
// DocsDir does for docs (docs/PLANS.md).
func PlansDir(home string) string {
	return filepath.Join(home, "plans")
}

// TerminalDir holds what terminal id keeps on disk under home: its spool
// and history (docs/ATTACH.md). Terminal output can hold secrets: only the
// user may read it.
func TerminalDir(home, id string) string {
	return filepath.Join(TerminalsDir(home), id)
}

// TerminalsDir holds the directory of each terminal under home.
func TerminalsDir(home string) string {
	return filepath.Join(home, "terminals")
}

// AgentsDir holds the directory of each agent under home.
func AgentsDir(home string) string {
	return filepath.Join(home, "agents")
}

// AgentDir holds the backup of agent id's conversation under home. It can hold
// secrets: only the user may read it.
func AgentDir(home, id string) string {
	return filepath.Join(AgentsDir(home), id)
}

// TrashAgentsDir holds what is kept of removed agents under home, until it
// expires.
func TrashAgentsDir(home string) string {
	return filepath.Join(home, "trash", "agents")
}

// HubDir holds this node's login to a hub under home: its key, its
// certificate, the hub's CA and login.json (the wdx doc HUB_LOGIN). The key
// is the node's identity: only the user may enter it.
func HubDir(home string) string {
	return filepath.Join(home, "hub")
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
	// ReleasesAPI answers which stable release is the latest.
	ReleasesAPI = "https://api.github.com/repos/srjn45/wdx/releases"
	// DevLatestURL names the newest release of either kind, kept by
	// scripts/publish-latest.sh; GitHub has no such answer of its own.
	DevLatestURL = "https://raw.githubusercontent.com/srjn45/wdx/channels/LATEST-dev"
)

// PreviousDir holds the version an update replaced, and the database as it
// was, until the next update or a rollback.
func PreviousDir(home string) string {
	return filepath.Join(home, "previous")
}

// TideLockPath is the file the running tide holds locked under home, as a
// daemon has one tide; it holds the tide's pid.
func TideLockPath(home string) string {
	return filepath.Join(home, "tide.lock")
}

// UpdateLockPath is the file an update or rollback holds locked under home.
func UpdateLockPath(home string) string {
	return filepath.Join(home, "update.lock")
}
