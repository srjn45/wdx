package hubconfig

import (
	"os"
	"path/filepath"
)

// ConfigPath is the config file under home.
func ConfigPath(home string) string { return filepath.Join(home, "config.yaml") }

// ConfigLockPath is the file held locked while the config file is rewritten.
func ConfigLockPath(home string) string { return filepath.Join(home, "config.lock") }

// DBPath is the hub's database under home, beside its -wal and -shm.
func DBPath(home string) string { return filepath.Join(home, "hub.db") }

// CADir is the directory of the certificate authority's keys and
// certificates under home.
func CADir(home string) string { return filepath.Join(home, "ca") }

// SocketPath is the Unix socket the admin endpoint listens on under home.
func SocketPath(home string) string { return filepath.Join(home, Name+".sock") }

// LockPath is the file the running hub holds locked under home: one serve
// per home.
func LockPath(home string) string { return filepath.Join(home, Name+".lock") }

// LogsDir is where the commands other than serve write their daily logs.
func LogsDir(home string) string { return filepath.Join(home, "logs") }

// MakeHome creates home if it is missing and makes it the user's alone,
// mode 0700: it holds the certificate authority's key.
func MakeHome(home string) error {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	return os.Chmod(home, 0o700)
}
