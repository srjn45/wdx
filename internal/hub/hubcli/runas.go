package hubcli

import (
	"github.com/srjn45/wdx/internal/hub/hubconfig"
)

// RunAs is who a command continues as, as main is to make it.
type RunAs struct {
	// Switch is true when the command becomes Account: its uid, gid and
	// groups.
	Switch  bool
	Account hubconfig.Account
	// Home is the HOME the command continues with; "" leaves it.
	Home string
}

// Decide says who a command run with args by the user uid continues as. Run
// as root on a machine with the installed hub, whose user is hub, it becomes
// hub, so that it reads the hub's home and what it writes there is the
// hub's; update with --apply or --rollback stays root, which replacing the
// binary and the service needs, with hub's home directory, and runs as hub
// what it does in the hub's home. Otherwise it runs as it started. hub is
// nil when the hub is not installed or its user is missing.
func Decide(args []string, uid int, hub *hubconfig.Account) RunAs {
	if uid != 0 || hub == nil {
		return RunAs{}
	}
	if switchesRelease(args) {
		return RunAs{Home: hub.Home}
	}
	return RunAs{Switch: true, Account: *hub, Home: hub.Home}
}

// switchesRelease is true when args run update with --apply or --rollback.
func switchesRelease(args []string) bool {
	cmd, rest, err := newRoot().Find(args)
	if err != nil || cmd.CommandPath() != name+" update" || cmd.ParseFlags(rest) != nil {
		return false
	}
	for _, flag := range []string{"apply", "rollback"} {
		if on, err := cmd.Flags().GetBool(flag); err == nil && on {
			return true
		}
	}
	return false
}
