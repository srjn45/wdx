package main

import (
	"context"
	"os"

	"github.com/srjn45/wdx/internal/hub/hubcli"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
)

func main() {
	env := hubcli.Env{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		Getenv: os.Getenv, //nolint:forbidigo // main is the one reader of the environment; the finish task adds cmd/wdx-hub to the rule's exception
		// The conventions of terminals, not settings of wdx-hub: https://no-color.org
		NoColor: os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb", //nolint:forbidigo // as above
	}
	cfg, err := hubconfig.Load(os.Getenv) //nolint:forbidigo // as above
	if err != nil && !hubcli.RewritesSettings(os.Args[1:], err) {
		os.Exit(hubcli.ConfigError(os.Args[1:], env, err))
	}
	env.Config = cfg
	// Without it only the relative paths given to backup and setting write fail.
	env.WorkDir, _ = os.Getwd()
	os.Exit(hubcli.Run(context.Background(), os.Args[1:], env))
}
