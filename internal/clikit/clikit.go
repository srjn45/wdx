// Package clikit is the framework every wdx program's commands run on: --json
// with exactly one object on stdout, problems with a stable code and exit
// codes 0 to 3, help in wdx's layout from the commands' annotations, a
// completion for every flag, prompts only where a person can answer them, and
// the log lines command received, command started and command returned. It
// knows no program: the name it prints, the error codes, the settings and the
// program's own errors come in through Env.
package clikit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/settings"
	"github.com/srjn45/wdx/internal/style"
)

// Program is what the framework says of the program it runs: the name on
// every line it prints, the version and commit it logs.
type Program struct{ Name, Version, Commit string }

// Logs is where a run's lines go: daily files <Program.Name>-<date>.log in Dir.
type Logs struct {
	Dir string
	// Writer, when set, takes the lines in place of the files in Dir: a
	// server's output, which the service manager running it keeps.
	Writer        io.Writer
	Level         slog.Leveler
	RetentionDays int
	// Process names the process on every line; cli when empty.
	Process string
}

// SettingValue is one setting as command received logs it: its value, as
// Shown gives it ([secret] for a secret), and where it came from.
type SettingValue struct{ Key, Value, From string }

// Env is everything a command takes from the process it runs in.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Program        Program
	Logs           Logs
	Settings       []SettingValue
	// User says who the program runs as, when it chose: logged with
	// command received.
	User    string
	NoColor bool   // NO_COLOR is set or TERM is dumb; turns off color mode auto only
	WorkDir string // the working directory, which Absolute makes paths against
	// Codes says what an error code of the program means: its HTTP status,
	// exit code and description. usage and internal are the framework's,
	// which every program's table has.
	Codes func(code string) (CodeInfo, bool)
	// Problem maps the program's own errors to its problems; the framework
	// maps its own (usage, settings, a panic) and everything else to internal.
	Problem func(err error) (Problem, bool)
}

func (env Env) code(code string) CodeInfo {
	info, _ := env.Codes(code)
	return info
}

func (env Env) newProblem(code, detail string) Problem {
	return NewProblem(code, env.code(code), detail)
}

// Invocation is what Run knows of the command it runs, for the commands.
type Invocation struct {
	DebugID string
	Quiet   bool // a shell asks for completions; a server asked logs at debug
	Env     Env

	started  bool // RunE was reached, so the command line was valid
	response any  // what the command printed
}

type invocationKey struct{}

// InvocationOf returns the Invocation of the run cmd is part of.
func InvocationOf(cmd *cobra.Command) *Invocation {
	if ctx := cmd.Context(); ctx != nil {
		if inv, ok := ctx.Value(invocationKey{}).(*Invocation); ok {
			return inv
		}
	}
	return &Invocation{}
}

// Run executes the command line args and returns the exit code. newRoot
// builds the tree for this run, since cobra's flags hold the values parsed
// into them. The root's context derives from ctx, so a program's own state
// for the run rides in it.
func Run(ctx context.Context, args []string, env Env, newRoot func() *cobra.Command) int {
	start := time.Now()
	debugID := logging.NewDebugID()
	logFile, log := newLogger(env, debugID)
	defer logFile.Close()
	// A shell asks on every Tab: worth logging only when debugging.
	completion := isCompletion(args)
	var printed bytes.Buffer
	if completion {
		log = logging.Quiet(log)
		env.Stdout = io.MultiWriter(env.Stdout, &printed)
	}
	logReceived(log, args, env)

	root := newRoot()
	if completion {
		registerCompletions(root)
	}
	root.SetArgs(args)
	root.SetIn(env.Stdin)
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	wrapRun(root)
	inv := &Invocation{Env: env, DebugID: debugID, Quiet: completion}
	root.SetContext(logging.NewContext(context.WithValue(ctx, invocationKey{}, inv), log))
	cmd, err := root.ExecuteC()
	if completion {
		inv.response = completionResponse(printed.String())
	}
	if err == nil {
		logReturned(log, cmd, start, ExitOK, "", inv.response, nil)
		return ExitOK
	}
	// Cobra returns usage errors (bad flags or arguments) before RunE starts.
	if !inv.started {
		err = &UsageError{err}
	}

	p := problemOf(env, err)
	exit := env.code(p.Code).Exit
	p.DebugId = debugID
	if p.Code == CodeUsage {
		p.Hint = withHelpHint(p.Hint, cmd)
	}
	logReturned(log, cmd, start, exit, p.Code, p, err)
	asJSON := wantsJSON(cmd, args)
	printError(env, asJSON, style.New(!asJSON && colorOn(cmd, env, env.Stderr)), p, cmd.Annotations[FailurePrefix])
	return exit
}

// ConfigError reports the settings that failed to load, before any command
// runs, and returns the exit code.
func ConfigError(args []string, env Env, err error) int {
	p := env.newProblem(CodeUsage, err.Error())
	hint := "fix or remove them"
	p.Hint = &hint
	if e, ok := errors.AsType[*settings.Error](err); ok {
		p = settingsProblem(env, e)
	}
	asJSON := slices.Contains(args, "--json")
	// The color setting may be among those that failed.
	st := style.New(!asJSON && !env.NoColor && IsTerminal(env.Stderr))
	printError(env, asJSON, st, p, "")
	return env.code(CodeUsage).Exit
}

// RewritesSettings is true when args run a command of root that writes the
// config file, setting write or setting set, and err names only keys no
// setting has or required settings no source sets: such a command moves the
// ones out of the way and can set the others, so it runs on the settings that
// loaded.
func RewritesSettings(root *cobra.Command, args []string, err error) bool {
	e, ok := errors.AsType[*settings.Error](err)
	if !ok || !e.OnlyRewritable() {
		return false
	}
	cmd, _, err := root.Find(args)
	if err != nil {
		return false
	}
	path := cmd.CommandPath()
	return path == root.Name()+" setting write" || path == root.Name()+" setting set"
}

// printError writes p to stdout as JSON, or to stderr as text: what failed on
// the first line, then the hint and the debug id. Text given p.Lines prints
// the first in place of p's detail and the others under it, as a list of
// problems.
func printError(env Env, asJSON bool, st style.Style, p Problem, prefix string) {
	if asJSON {
		_ = WriteJSON(env.Stdout, p)
		return
	}
	msg := p.Detail
	if len(p.Lines) > 0 {
		msg = p.Lines[0]
	}
	if prefix != "" {
		msg = prefix + ": " + msg
	}
	fmt.Fprintf(env.Stderr, "%s %s  %s\n", st.Failure(env.Program.Name+":"), msg, st.Failure("["+p.Code+"]"))
	for _, line := range p.Lines[min(1, len(p.Lines)):] {
		fmt.Fprintf(env.Stderr, "  %s\n", line)
	}
	if p.Hint != nil {
		fmt.Fprintf(env.Stderr, "  %s %s\n", st.Hint("hint:"), HintText(st, env.Program.Name, *p.Hint))
	}
	if p.DebugId != "" {
		fmt.Fprintf(env.Stderr, "  %s\n", st.Muted("debug_id: "+p.DebugId))
	}
}

// HintText styles the command lines of program and the flags in hint.
func HintText(st style.Style, program, hint string) string {
	commandOrFlag := regexp.MustCompile(regexp.QuoteMeta(program) + `( [a-z][a-z-]*)*|--[a-z][a-z-]*`)
	return commandOrFlag.ReplaceAllStringFunc(hint, func(m string) string {
		if strings.HasPrefix(m, "--") {
			return st.Flag(m)
		}
		return st.Command(m)
	})
}

// wrapRun makes every RunE in the tree log its start, check the flags --json
// requires and turn a panic into an error.
func wrapRun(cmd *cobra.Command) {
	if run := cmd.RunE; run != nil {
		cmd.RunE = func(cmd *cobra.Command, args []string) (err error) {
			logStarted(cmd, args)
			if err := CheckJSONRequires(cmd); err != nil {
				return err
			}
			InvocationOf(cmd).started = true
			defer func() {
				if r := recover(); r != nil {
					err = &panicError{value: r, stack: debug.Stack()}
				}
			}()
			return run(cmd, args)
		}
	}
	for _, sub := range cmd.Commands() {
		wrapRun(sub)
	}
}

// panicError is a panic recovered while a command ran.
type panicError struct {
	value any
	stack []byte
}

func (e *panicError) Error() string { return fmt.Sprintf("panic: %v", e.value) }

// CheckJSONRequires fails when --json is given without one of the flags that
// answer the command's prompts.
func CheckJSONRequires(cmd *cobra.Command) error {
	if !JSONMode(cmd) {
		return nil
	}
	for _, rule := range jsonRules(cmd) {
		if rule.When != nil && !cmd.Flags().Changed(*rule.When) || rule.Unless != nil && cmd.Flags().Changed(*rule.Unless) {
			continue
		}
		if slices.ContainsFunc(rule.Flags, cmd.Flags().Changed) {
			continue
		}
		flags := "--" + strings.Join(rule.Flags, " or --")
		given := "--json"
		if rule.When != nil {
			given += " with --" + *rule.When
		}
		return &Hinted{&UsageError{fmt.Errorf("%s requires %s", given, flags)}, "pass " + flags}
	}
	return nil
}

func withHelpHint(hint *string, cmd *cobra.Command) *string {
	if cmd.Name() == "help" && cmd.HasParent() {
		cmd = cmd.Parent()
	}
	h := fmt.Sprintf("see '%s --help'", cmd.CommandPath())
	if hint != nil {
		h = *hint + "; " + h
	}
	return &h
}

// wantsJSON reports whether --json was given; args are checked too, since a
// usage error may stop cobra before it parses the flag.
func wantsJSON(cmd *cobra.Command, args []string) bool {
	if f := cmd.Flags().Lookup("json"); f != nil && f.Changed {
		return f.Value.String() == "true"
	}
	return slices.Contains(args, "--json")
}
