package holder

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

// defaultShell is run when the user database names no login shell.
const defaultShell = "/bin/sh"

// Account is the user wdx runs as, from the user database.
type Account struct {
	Name  string
	Home  string
	shell string // "" when the database names none
}

// LoginShell is the absolute path of the user's login shell, /bin/sh when
// the user database names none.
func (a Account) LoginShell() string {
	return cmp.Or(a.shell, defaultShell)
}

// LookupAccount reads the current user from the user database: through
// getent on Linux, so directory services count, and dscl on macOS.
func LookupAccount(ctx context.Context) (Account, error) {
	u, err := user.Current()
	if err != nil {
		return Account{}, fmt.Errorf("look up the current user: %w", err)
	}
	a := Account{Name: u.Username, Home: u.HomeDir}
	if shell := loginShell(ctx, u); filepath.IsAbs(shell) {
		a.shell = shell
	}
	return a, nil
}

func loginShell(ctx context.Context, u *user.User) string {
	if runtime.GOOS == "darwin" {
		// "UserShell: /bin/zsh"
		out, err := exec.CommandContext(ctx, "dscl", ".", "-read", "/Users/"+u.Username, "UserShell").Output()
		if err != nil {
			return ""
		}
		_, shell, _ := strings.Cut(strings.TrimSpace(string(out)), ": ")
		return shell
	}
	if out, err := exec.CommandContext(ctx, "getent", "passwd", u.Uid).Output(); err == nil {
		return passwdShell(strings.TrimSpace(string(out)), u.Uid)
	}
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return ""
	}
	defer f.Close()
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		if shell := passwdShell(lines.Text(), u.Uid); shell != "" {
			return shell
		}
	}
	return ""
}

// passwdShell returns the shell of a passwd line, name:x:uid:gid:gecos:home:shell,
// when it is the user uid's.
func passwdShell(line, uid string) string {
	fields := strings.Split(line, ":")
	if len(fields) != 7 || fields[2] != uid {
		return ""
	}
	return fields[6]
}
