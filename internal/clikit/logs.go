package clikit

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/srjn45/wdx/internal/logging"
)

// newLogger returns the run's log, a file unless Logs.Writer is set, and a
// logger writing to it that tags every record with debugID and the process.
func newLogger(env Env, debugID string) (io.Closer, *slog.Logger) {
	var w io.WriteCloser = nopCloser{env.Logs.Writer}
	if env.Logs.Writer == nil {
		f := logging.NewDailyFile(env.Logs.Dir, env.Program.Name, env.Logs.RetentionDays, time.Now)
		if env.Logs.Owner != nil {
			f.SetOwner(*env.Logs.Owner)
		}
		w = f
	}
	return w, logging.New(w, env.Logs.Level).With("debug_id", debugID, "pid", os.Getpid(), "process", cmp.Or(env.Logs.Process, "cli"))
}

// nopCloser is a writer the run does not own, left open when it ends.
type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// logReceived logs the command line as given, with the flags root's command
// measures measured, and what else decides how it runs: every setting with
// where it came from.
func logReceived(log *slog.Logger, root *cobra.Command, args []string, env Env) {
	var settings []any
	for _, s := range env.Settings {
		settings = append(settings, slog.Group(s.Key, "value", s.Value, "from", s.From))
	}
	log.Info("command received",
		"args", ArgsLogged(root, args),
		"version", env.Program.Version,
		"commit", env.Program.Commit,
		"cwd", env.WorkDir,
		"stdin_terminal", IsTerminal(env.Stdin),
		"no_color", env.NoColor,
		slog.Group("settings", settings...),
		user(env.User))
}

// user is the user a run runs as, when its program says.
func user(name string) slog.Attr {
	if name == "" {
		return slog.Attr{}
	}
	return slog.String("user", name)
}

// PromptFlag and CodeFlag are the flags whose values are never logged, only
// measured: a prompt can hold secrets, and a code is one.
const (
	PromptFlag = "prompt"
	CodeFlag   = "code"
)

// secretFlags are the flags measured in the log of every command.
var secretFlags = []string{PromptFlag, CodeFlag}

// measuredAnnotation marks a flag of one command that is measured in the log.
const measuredAnnotation = "clikit_measured"

// MeasureFlag has cmd's flag name measured in the log instead of logged, in
// command received and command started: its value can be a secret.
func MeasureFlag(cmd *cobra.Command, name string) {
	if err := cmd.Flags().SetAnnotation(name, measuredAnnotation, []string{"true"}); err != nil {
		panic(err)
	}
}

func isMeasured(f *pflag.Flag) bool {
	return slices.Contains(secretFlags, f.Name) || f.Annotations[measuredAnnotation] != nil
}

// ArgsLogged is args as command received logs them: with the values of
// --prompt, --code and the flags the command args run in root measures
// measured instead.
func ArgsLogged(root *cobra.Command, args []string) []string {
	flags := slices.Clone(secretFlags)
	find := args
	if isCompletion(args) {
		find = args[1:]
	}
	if cmd, _, err := root.Find(find); err == nil {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if isMeasured(f) && !slices.Contains(flags, f.Name) {
				flags = append(flags, f.Name)
			}
		})
	}
	logged := slices.Clone(args)
	for i, arg := range logged {
		for _, flag := range flags {
			if arg == "--"+flag && i+1 < len(logged) {
				logged[i+1] = measured(logged[i+1])
			}
			if value, ok := strings.CutPrefix(arg, "--"+flag+"="); ok {
				logged[i] = "--" + flag + "=" + measured(value)
			}
		}
	}
	return logged
}

func measured(value string) string {
	return fmt.Sprintf("<%d bytes>", len(value))
}

// logStarted logs the command cobra resolved and the value of every flag.
func logStarted(cmd *cobra.Command, args []string) {
	flags := map[string]string{}
	set := []string{}
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		flags[f.Name] = f.Value.String()
		if isMeasured(f) {
			flags[f.Name] = measured(f.Value.String())
		}
		if f.Changed {
			set = append(set, f.Name)
		}
	})
	slices.Sort(set)
	logging.FromContext(cmd.Context()).Info("command started",
		"command", cmd.CommandPath(),
		"args", args,
		"flags", flags,
		"flags_set", set,
		"color_stdout", StdoutStyle(cmd).On(),
		"color_stderr", StderrStyle(cmd).On())
}

var outcomes = map[int]string{ExitOK: "ok", ExitRejected: "rejected", ExitUsage: "usage", ExitSystem: "system"}

// logReturned logs what the command returned: response is exactly what it
// printed, or would have printed as JSON, and code its error code if it
// failed.
func logReturned(log *slog.Logger, cmd *cobra.Command, start time.Time, exit int, code string, response any, err error) {
	attrs := []any{"command", cmd.CommandPath(), "outcome", outcomes[exit], "exit", exit, logging.Duration(start)}
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelError
		attrs = append(attrs, "code", code)
		if p, ok := errors.AsType[*panicError](err); ok {
			attrs = append(attrs, "stack", string(p.stack))
		}
	}
	log.Log(context.Background(), level, "command returned", append(attrs, "response", response)...)
}

// LogAnswer logs how a question that decides what a command does was
// answered: by a flag or at the prompt.
func LogAnswer(cmd *cobra.Command, question, answer, by string) {
	logging.FromContext(cmd.Context()).Info("question answered", "question", question, "answer", answer, "by", by)
}

// LogUnanswered logs a question that could not be asked.
func LogUnanswered(cmd *cobra.Command, question string) {
	logging.FromContext(cmd.Context()).Info("question unanswered", "question", question, "reason", NoPromptReason(cmd))
}
