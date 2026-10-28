// Package holdertest lets a test binary be its own holder: Start runs the
// wdx binary as "wdx holder", and in tests the binary is the test's.
package holdertest

import (
	"context"
	"flag"
	"os"
	"slices"
	"strings"

	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/identity"
)

// RunIfHolder runs a holder and exits when the test binary was started as
// one; call it first in TestMain. Its home comes from the environment, as
// in wdx, and every other variable is passed on to the shell.
func RunIfHolder() {
	if len(os.Args) < 2 || os.Args[1] != "holder" {
		return
	}
	flags := flag.NewFlagSet("holder", flag.ContinueOnError)
	var o holder.Options
	flags.StringVar(&o.ID, "id", "", "")
	flags.StringVar(&o.Project, "project", "", "")
	flags.StringVar(&o.Dir, "dir", "", "")
	flags.StringVar(&o.Shell, "shell", "", "")
	if flags.Parse(os.Args[2:]) != nil {
		os.Exit(2)
	}
	homeVar := identity.Env("HOME")
	o.Socket = identity.HolderSocket(os.Getenv(homeVar), o.ID) //nolint:forbidigo // the test binary is this holder's main, which reads its home as cmd/wdx does
	o.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, homeVar+"=") })
	o.Ready = os.NewFile(3, "ready")
	if holder.Run(context.Background(), o) != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
