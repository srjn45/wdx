package cli

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/srjn45/warden/internal/api"
	"github.com/srjn45/warden/internal/config"
	"github.com/srjn45/warden/internal/identity"
	"github.com/srjn45/warden/internal/logging"
)

// newLogger returns the log file under the configured home and a logger
// writing to it that tags every record with debugID.
func newLogger(cfg config.Config, debugID string) (*logging.DailyFile, *slog.Logger) {
	file := logging.NewDailyFile(identity.LogsDir(cfg.Home), cfg.LogRetentionDays, time.Now)
	return file, logging.New(file, cfg.LogLevel).With("debug_id", debugID, "pid", os.Getpid(), "process", "cli")
}

// logReceived logs the command line as given and what else decides how it
// runs: every setting with where it came from.
func logReceived(log *slog.Logger, args []string, env Env) {
	var settings []any
	for _, s := range config.Settings() {
		settings = append(settings, slog.Group(s.Key, "value", env.Config.Value(s.Key), "from", env.Config.From[s.Key]))
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = err.Error()
	}
	log.Info("command received",
		"args", args,
		"version", identity.Version,
		"commit", identity.Commit,
		"cwd", cwd,
		"stdin_terminal", isTerminal(env.Stdin),
		slog.Group("settings", settings...))
}

// logStarted logs the command cobra resolved and the value of every flag.
func logStarted(cmd *cobra.Command, args []string) {
	flags := map[string]string{}
	set := []string{}
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		flags[f.Name] = f.Value.String()
		if f.Changed {
			set = append(set, f.Name)
		}
	})
	slices.Sort(set)
	logging.FromContext(cmd.Context()).Info("command started",
		"command", cmd.CommandPath(),
		"args", args,
		"flags", flags,
		"flags_set", set)
}

var outcomes = map[int]string{api.ExitOK: "ok", api.ExitRejected: "rejected", api.ExitUsage: "usage", api.ExitSystem: "system"}

// logReturned logs what the command returned: response is exactly what it
// printed, or would have printed as JSON.
func logReturned(log *slog.Logger, cmd *cobra.Command, start time.Time, exit int, response any, err error) {
	attrs := []any{"command", cmd.CommandPath(), "outcome", outcomes[exit], "exit", exit, logging.Duration(start)}
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelError
		if p, ok := errors.AsType[*panicError](err); ok {
			attrs = append(attrs, "stack", string(p.stack))
		}
	}
	log.Log(context.Background(), level, "command returned", append(attrs, "response", response)...)
}

// logAnswer logs how a question that decides what a command does was
// answered: by a flag or at the prompt.
func logAnswer(cmd *cobra.Command, question, answer, by string) {
	logging.FromContext(cmd.Context()).Info("question answered", "question", question, "answer", answer, "by", by)
}

// logUnanswered logs a question that could not be asked.
func logUnanswered(cmd *cobra.Command, question string) {
	logging.FromContext(cmd.Context()).Info("question unanswered", "question", question, "reason", noPromptReason(cmd))
}
