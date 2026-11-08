package hubcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hub/server"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/logging"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the hub in the foreground until it is stopped",
		Long: "The hub serves its web endpoint on listen.web, its node endpoint on listen.nodes\n" +
			"and its admin socket in the home, until SIGINT or SIGTERM; then it lets the\n" +
			"requests in flight finish. Its first start creates the database and the\n" +
			"certificate authority. It logs JSON lines to stdout, where a container or a\n" +
			"service manager keeps them, so it has no --json. One hub serves a home.",
		Example: clikit.Examples(
			"Run the hub", name+" serve",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not serving",
			clikit.OutputNote: "Its log, as JSON lines on stdout; on stderr, once it listens:\n" +
				"serving on <web> (web), <nodes> (nodes) and <socket> (admin)",
			clikit.Errors: clikit.CodeList(hubapi.ErrorCodeAlreadyRunning),
		},
		RunE: runServe,
	}
}

func runServe(cmd *cobra.Command, _ []string) error {
	if clikit.JSONMode(cmd) {
		return &clikit.UsageError{Err: errors.New("serve logs JSON lines to stdout and has no --json")}
	}
	run := runOf(cmd)
	cfg := run.env.Config
	if err := checkTLSFiles(cfg); err != nil {
		return err
	}
	stop := run.env.Serve.Stop
	if stop == nil {
		stop = func(ctx context.Context) (context.Context, context.CancelFunc) {
			return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		}
	}
	ctx, cancel := stop(cmd.Context())
	defer cancel()
	getenv := homeEnv(run.env)
	s, err := server.Listen(ctx, server.Options{
		Config:   cfg,
		Log:      logging.New(run.output, &run.level).With("pid", os.Getpid(), "process", "hub"),
		LogLevel: &run.level,
		Reload:   func() (hubconfig.Config, error) { return hubconfig.Load(getenv) },
		Settings: settingStore{getenv: getenv},
		// A zero value takes the server's default.
		ShutdownTimeout: run.env.Serve.ShutdownTimeout,
		Now:             run.env.Serve.Now,
	})
	if err != nil {
		return err
	}
	a := s.Addresses()
	st := clikit.StderrStyle(cmd)
	fmt.Fprintf(cmd.ErrOrStderr(), "%s on %s (web), %s (nodes) and %s (admin)\n", st.Success("serving"), st.Name(a.Web), st.Name(a.Nodes), st.Name(a.Admin))
	if run.env.Serve.Serving != nil {
		run.env.Serve.Serving(a)
	}
	return s.Serve(ctx)
}

// checkTLSFiles fails, as a setting that cannot be used, when web.tls is
// files and one of them is not there.
func checkTLSFiles(cfg hubconfig.Config) error {
	if cfg.WebTLS != hubconfig.TLSFiles {
		return nil
	}
	for key, path := range map[string]string{"web.tls_cert": cfg.WebTLSCert, "web.tls_key": cfg.WebTLSKey} {
		if _, err := os.Stat(path); err != nil {
			return &clikit.Hinted{
				Err:  &clikit.UsageError{Err: fmt.Errorf("%s is %s, which cannot be read: %w", key, path, err)},
				Hint: "put the file there, or change " + key + " with " + name + " setting set",
			}
		}
	}
	return nil
}

// homeEnv is the environment of env, as the settings read it again, with the
// home it was loaded with.
func homeEnv(env Env) func(string) string {
	return func(key string) string {
		if key == hubconfig.EnvPrefix+"HOME" {
			return env.Config.Home
		}
		if env.Getenv == nil {
			return ""
		}
		return env.Getenv(key)
	}
}
