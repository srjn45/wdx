package main

import (
	"context"
	"os"
	"os/exec"
	"runtime"

	"github.com/srjn45/wdx/internal/cli"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/service"
)

func main() {
	env := cli.Env{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, LookPath: exec.LookPath,
		// The conventions of terminals, not settings of wdx: https://no-color.org
		NoColor:  os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb",
		Terminal: cli.TerminalEnv(os.Environ()),
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		os.Exit(cli.ConfigError(os.Args[1:], env, err))
	}
	env.Config = cfg
	// Without it only starting the daemon in the background fails.
	env.Executable, _ = os.Executable()
	// The service runs the daemon for the default home only.
	if userHome, err := os.UserHomeDir(); err == nil && cfg.Home == config.Default().Home {
		env.Service = service.Installed(runtime.GOOS, userHome)
	}
	os.Exit(cli.Run(context.Background(), os.Args[1:], env))
}
