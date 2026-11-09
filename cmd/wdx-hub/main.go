package main

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"

	"github.com/srjn45/wdx/internal/hub/hubcli"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
)

func main() {
	args := os.Args[1:]
	env := hubcli.Env{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		Getenv: os.Getenv,
		// The conventions of terminals, not settings of wdx-hub: https://no-color.org
		NoColor: os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb",
	}
	// Before the settings, which are read from the home of the user it runs as.
	if err := runAs(args, &env); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", hubconfig.Name, err)
		os.Exit(3)
	}
	cfg, err := hubconfig.Load(os.Getenv)
	if err != nil && !hubcli.RewritesSettings(args, err) {
		os.Exit(hubcli.ConfigError(args, env, err))
	}
	env.Config = cfg
	// Without it only the relative paths given to backup and setting write fail.
	env.WorkDir, _ = os.Getwd()
	os.Exit(hubcli.Run(context.Background(), args, env))
}

// runAs makes the process the user hubcli.Decide says, and records in env who
// it runs as.
func runAs(args []string, env *hubcli.Env) error {
	uid := os.Geteuid()
	var hub *hubconfig.Account
	var missing error
	if uid == 0 && hubconfig.Installed.Present() {
		a, err := hubconfig.LookupAccount(hubconfig.Installed.User)
		if err == nil {
			hub = &a
		}
		missing = err
	}
	as := hubcli.Decide(args, uid, hub)
	if as.Home != "" {
		if err := os.Setenv("HOME", as.Home); err != nil {
			return err
		}
	}
	if as.Switch {
		a := as.Account
		if err := syscall.Setgroups(a.Groups); err != nil {
			return fmt.Errorf("become %s: groups: %w", a.Name, err)
		}
		if err := syscall.Setgid(a.GID); err != nil {
			return fmt.Errorf("become %s: gid: %w", a.Name, err)
		}
		if err := syscall.Setuid(a.UID); err != nil {
			return fmt.Errorf("become %s: uid: %w", a.Name, err)
		}
	}
	env.Root = os.Geteuid() == 0
	env.Account = hub
	env.User = userName(os.Geteuid())
	switch {
	case as.Switch:
		env.User += ", become from root"
	case as.Home != "":
		env.User += ", for the update, with the home of " + hub.Name
	case missing != nil:
		env.User += ", as the installed hub's user " + hubconfig.Installed.User + " is not found: " + missing.Error()
	}
	return nil
}

func userName(uid int) string {
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		return u.Username
	}
	return "uid " + strconv.Itoa(uid)
}
