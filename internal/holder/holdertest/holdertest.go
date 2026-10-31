// Package holdertest lets a test binary be its own holder: Start runs the
// wdx binary as "wdx holder", and in tests the binary is the test's.
package holdertest

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/waittest"
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
	flags.StringVar(&o.AgentID, "agent-id", "", "")
	var acaID string
	flags.StringVar(&acaID, "aca", "", "")
	flags.Func("command", "", func(word string) error {
		o.Command = append(o.Command, word)
		return nil
	})
	flags.Int64Var(&o.HistoryLines, "history-lines", 0, "")
	flags.DurationVar(&o.WriteTimeout, "write-timeout", 0, "")
	if flags.Parse(os.Args[2:]) != nil {
		os.Exit(2)
	}
	homeVar := identity.Env("HOME")
	o.Home = os.Getenv(homeVar) //nolint:forbidigo // the test binary is this holder's main, which reads its home as cmd/wdx does
	o.Socket = identity.HolderSocket(o.Home, o.ID)
	o.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, homeVar+"=") })
	o.Ready = os.NewFile(3, "ready")
	// It logs where a holder wdx starts does: the home's log files.
	logs := logging.NewDailyFile(identity.LogsDir(o.Home), 7, time.Now)
	log := logging.New(logs, slog.LevelDebug).With("pid", os.Getpid(), "process", "holder", "terminal_id", o.ID)
	if known, ok := aca.ByID(acaID); ok {
		o.ParseScreen = known.ScreenParser().Parse
		o.Deliver = known.Deliver
		o.Interrupt = known.Interrupt()
	}
	err := holder.Run(logging.NewContext(context.Background(), log), o)
	logs.Close() // os.Exit runs no deferred call
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// Limits are limits a loaded machine does not reach: a test fails on what it
// checks, or at its deadline, not because a process was held back.
func Limits(t *testing.T) holder.Limits {
	limit := Patience(t)
	return holder.Limits{Ready: limit, Dial: limit, Write: limit}
}

// Patience is such a limit for any other wait on a process; 0, wdx's own
// limit, in a run that keeps those.
func Patience(t *testing.T) time.Duration {
	if runtest.ProductionLimits() {
		return 0
	}
	return waittest.Limit(t)
}
