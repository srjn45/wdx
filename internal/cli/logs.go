package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/srjn45/warden/internal/identity"
	"github.com/srjn45/warden/internal/logging"
)

const defaultRetentionDays = 7

// logSettings are the environment variables that configure logging.
var logSettings = []string{identity.Env("LOG_LEVEL"), identity.Env("LOG_RETENTION_DAYS")}

// newLogger returns the log file under home and a logger writing to it that
// tags every record with debugID. Bad settings fall back to the defaults and
// are logged as warnings.
func newLogger(env Env, home, debugID string) (*logging.DailyFile, *slog.Logger) {
	level, levelErr := parseLevel(env.Getenv(logSettings[0]))
	retention, retentionErr := parseRetention(env.Getenv(logSettings[1]))
	file := logging.NewDailyFile(identity.LogsDir(home), retention, time.Now)
	log := logging.New(file, level).With("debug_id", debugID, "pid", os.Getpid())
	for i, err := range []error{levelErr, retentionErr} {
		if err != nil {
			log.Warn("log setting ignored", "setting", logSettings[i], "err", err.Error())
		}
	}
	return file, log
}

func parseLevel(s string) (slog.Level, error) {
	var level slog.Level
	if s == "" {
		return slog.LevelInfo, nil
	}
	if err := level.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo, err
	}
	return level, nil
}

func parseRetention(s string) (int, error) {
	if s == "" {
		return defaultRetentionDays, nil
	}
	days, err := strconv.Atoi(s)
	if err == nil && days < 1 {
		err = fmt.Errorf("%d is less than 1", days)
	}
	if err != nil {
		return defaultRetentionDays, err
	}
	return days, nil
}

// logReceived logs the command line as given and what else decides how it
// runs.
func logReceived(log *slog.Logger, args []string, env Env, home string) {
	settings := []any{}
	for _, key := range append([]string{identity.Env("HOME"), identity.Env("NO_INPUT")}, logSettings...) {
		settings = append(settings, slog.String(key, env.Getenv(key)))
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = err.Error()
	}
	log.Info("command received",
		"args", args,
		"version", identity.Version,
		"commit", identity.Commit,
		"home", home,
		"cwd", cwd,
		"stdin_terminal", isTerminal(env.Stdin),
		slog.Group("env", settings...))
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

var outcomes = map[int]string{exitOK: "ok", exitRejected: "rejected", exitUsage: "usage", exitSystem: "system"}

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
