// Package hubcli is wdx-hub's command tree: serve, which runs the hub, and the
// commands that manage it over its socket or its config file. It runs on
// clikit, as wdx's commands do.
package hubcli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hub/server"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

const name = hubconfig.Name

// Env is everything a command takes from the process it runs in.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Config         hubconfig.Config
	// Getenv reads the environment, as os.Getenv does, for reading the
	// settings again: the variables win over the config file.
	Getenv func(string) string
	// NoColor is true when the terminal asks for no color: NO_COLOR is set
	// or TERM is dumb.
	NoColor bool
	// WorkDir is the working directory, which relative paths are made
	// absolute against.
	WorkDir string
	// Serve are what serve takes besides the settings; tests set them.
	Serve ServeOptions
}

// ServeOptions are what tests change of serve.
type ServeOptions struct {
	// Stop is the context that ends serve, as SIGINT or SIGTERM do; nil
	// waits for those signals.
	Stop func(ctx context.Context) (context.Context, context.CancelFunc)
	// Serving is told where the hub listens once it does.
	Serving func(server.Addresses)
	// ShutdownTimeout is how long requests in flight get once serve is
	// stopped; server.DefaultShutdownTimeout when 0.
	ShutdownTimeout time.Duration
	// Now is the certificate authority's clock; time.Now when nil.
	Now func() time.Time
}

// Run executes the command line args and returns the process exit code.
func Run(ctx context.Context, args []string, env Env) int {
	run := &runState{env: env}
	fw := frameworkEnv(env)
	if servesToOutput(args) {
		// serve's lines are the hub's log: its output, which the container or
		// the service manager keeps.
		run.output = &lockedWriter{w: env.Stdout}
		run.level.Set(env.Config.LogLevel)
		fw.Logs = clikit.Logs{Writer: run.output, Level: &run.level, Process: "hub"}
	}
	return clikit.Run(context.WithValue(ctx, runKey{}, run), args, fw, newRoot)
}

// ConfigError reports the settings that failed to load, before any command
// runs, and returns the exit code.
func ConfigError(args []string, env Env, err error) int {
	return clikit.ConfigError(args, frameworkEnv(env), err)
}

// RewritesSettings is true when args run a command that writes the config
// file and err names only keys no setting has: such a command moves them out
// of the way, so it runs on the settings that loaded.
func RewritesSettings(args []string, err error) bool {
	return clikit.RewritesSettings(newRoot(), args, err)
}

// servesToOutput is true when args run serve, which logs to stdout; with
// --json it fails, and logs to the files as the other commands do.
func servesToOutput(args []string) bool {
	cmd, rest, err := newRoot().Find(args)
	if err != nil || cmd.CommandPath() != name+" serve" || cmd.ParseFlags(rest) != nil {
		return false
	}
	return !clikit.JSONMode(cmd)
}

// frameworkEnv is what the framework takes of env for the run of args.
func frameworkEnv(env Env) clikit.Env {
	var values []clikit.SettingValue
	for _, s := range hubconfig.Settings() {
		values = append(values, clikit.SettingValue{Key: s.Key, Value: env.Config.Shown(s.Key), From: env.Config.From[s.Key]})
	}
	return clikit.Env{
		Stdin: env.Stdin, Stdout: env.Stdout, Stderr: env.Stderr,
		Program: clikit.Program{Name: name, Version: identity.Version, Commit: identity.Commit},
		Logs: clikit.Logs{
			Dir:           hubconfig.LogsDir(env.Config.Home),
			Level:         env.Config.LogLevel,
			RetentionDays: env.Config.LogRetentionDays,
		},
		Settings: values,
		NoColor:  env.NoColor,
		WorkDir:  env.WorkDir,
		Codes: func(code string) (clikit.CodeInfo, bool) {
			info, ok := hubapi.Codes[hubapi.ErrorCode(code)]
			return info, ok
		},
		Problem: problem,
	}
}

// runState is what a run's commands share besides the framework's.
type runState struct {
	env Env
	// output and level are serve's log: stdout, shared by the run's lines and
	// the hub's, at the level a reload sets.
	output *lockedWriter
	level  slog.LevelVar
	client *client // the run's, made by clientOf
}

type runKey struct{}

// runOf returns the state of the run cmd is part of.
func runOf(cmd *cobra.Command) *runState {
	if ctx := cmd.Context(); ctx != nil {
		if r, ok := ctx.Value(runKey{}).(*runState); ok {
			return r
		}
	}
	return &runState{}
}

// envOf returns the Env of the run cmd is part of.
func envOf(cmd *cobra.Command) Env { return runOf(cmd).env }

// clientOf returns the client of the hub serving the configured home.
func clientOf(cmd *cobra.Command) *client {
	r := runOf(cmd)
	if r.client == nil {
		inv := clikit.InvocationOf(cmd)
		r.client = newClient(hubconfig.SocketPath(r.env.Config.Home), inv.DebugID, inv.Quiet)
	}
	return r.client
}

func newRoot() *cobra.Command {
	return clikit.NewRoot(clikit.Root{
		Use:    name,
		Short:  "Serve the hub that connects wdx machines",
		Long:   name + " serve runs the hub; backup and version ask it over its socket, and setting changes the config file it reads.",
		Color:  style.ColorAuto,
		Topics: topics,
	}, newServeCmd(), newVersionCmd(), newSettingCmd(), newBackupCmd())
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version of " + name + " and of the running hub",
		Example: clikit.Examples(
			"Show the versions of "+name+" and of the running hub", name+" version",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "The name, version and commit of " + name + " and of the running hub, null when it does not answer.\n" +
				`With --json: {"cli": {"name": ..., "version": ..., "commit": ...}, "hub": {"name": ..., "version": ..., "commit": ..., "api_version": ...}}`,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := versionResponse{CLI: cliVersion{Name: name, Version: identity.Version, Commit: identity.Commit}}
			v, err := clientOf(cmd).version(cmd.Context())
			if err != nil && !unavailable(err) {
				return err
			}
			if err == nil {
				r.Hub = &v
			}
			return clikit.PrintResult(cmd, r)
		},
	}
}

type cliVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

type versionResponse struct {
	CLI cliVersion      `json:"cli"`
	Hub *hubapi.Version `json:"hub"` // null when no hub answers
}

func (r versionResponse) Text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "%s %s %s\n", r.CLI.Name, st.Name(r.CLI.Version), st.Muted("("+r.CLI.Commit+")"))
	if r.Hub == nil {
		fmt.Fprintf(w, "hub %s\n", st.Warning("not running"))
		return
	}
	fmt.Fprintf(w, "hub %s %s %s\n", st.Name(r.Hub.Version), st.Muted("("+r.Hub.Commit+")"), st.Muted("api "+r.Hub.ApiVersion))
}
