// Package service finds the daemon installed as a service of the OS, as
// scripts/install.sh sets it up: a systemd user unit on Linux, a launchd
// agent on macOS.
package service

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/srjn45/warden/internal/identity"
	"github.com/srjn45/warden/internal/logging"
)

// The names scripts/install.sh installs the service under; vars because
// identity.Name is set at build time.
var (
	Unit  = identity.Name + ".service"
	Label = "com.github.srjn45." + identity.Name
)

// Manager starts and stops the daemon as a service.
type Manager struct {
	Name  string   // systemd or launchd
	Start []string // the command line that starts the daemon
	Stop  []string // the command line that stops it
	Logs  string   // where to read what the service manager saw
}

// Installed returns the manager the daemon is installed with for the user
// whose home directory is userHome, on the OS goos, or nil.
func Installed(goos, userHome string) *Manager {
	switch goos {
	case "linux":
		if exists(filepath.Join(userHome, ".config", "systemd", "user", Unit)) {
			return &Manager{
				Name:  "systemd",
				Start: []string{"systemctl", "--user", "start", Unit},
				Stop:  []string{"systemctl", "--user", "stop", Unit},
				Logs:  "journalctl --user -u " + Unit,
			}
		}
	case "darwin":
		if exists(filepath.Join(userHome, "Library", "LaunchAgents", Label+".plist")) {
			target := fmt.Sprintf("gui/%d/%s", os.Getuid(), Label)
			return &Manager{
				Name:  "launchd",
				Start: []string{"launchctl", "kickstart", target},
				// The agent restarts the daemon only when it fails, so a
				// signalled daemon, which exits cleanly, stays stopped.
				Stop: []string{"launchctl", "kill", "SIGTERM", target},
				Logs: filepath.Join(userHome, "Library", "Logs", identity.Name+".log"),
			}
		}
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Run runs one of the manager's command lines.
func (m *Manager) Run(ctx context.Context, args []string) error {
	out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
	out = bytes.TrimSpace(out)
	logging.FromContext(ctx).Info("service manager ran", "manager", m.Name, "args", args, "output", string(out), "ok", err == nil)
	if err != nil {
		return fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, out)
	}
	return nil
}
