package cli

// The cli's names for what moved to internal/clikit, so that its command
// files are unchanged while the framework moves. The callers task removes
// this file: the commands then use clikit's names directly.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

const (
	failurePrefix = clikit.FailurePrefix
	outputNote    = clikit.OutputNote
	resultList    = clikit.Results
	errorList     = clikit.Errors
	jsonRequires  = clikit.JSONRequires
	promptFlag    = clikit.PromptFlag
)

// usageError is clikit.UsageError under the cli's name, which errors.As
// finds as one.
type usageError struct {
	err error
}

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func (e *usageError) As(target any) bool {
	t, ok := target.(**clikit.UsageError)
	if ok {
		*t = &clikit.UsageError{Err: e.err}
	}
	return ok
}

// hinted is clikit.Hinted under the cli's name, which errors.As finds as
// one.
type hinted struct {
	err  error
	hint string
}

func (e *hinted) Error() string { return e.err.Error() }
func (e *hinted) Unwrap() error { return e.err }

func (e *hinted) As(target any) bool {
	t, ok := target.(**clikit.Hinted)
	if ok {
		*t = &clikit.Hinted{Err: e.err, Hint: e.hint}
	}
	return ok
}

// result is a response with the cli's text method.
type result interface {
	text(w io.Writer, st style.Style)
}

// textResult is r as the framework prints it: its JSON, and its text.
type textResult struct{ r result }

func (t textResult) Text(w io.Writer, st style.Style) { t.r.text(w, st) }

// MarshalJSON leaves escaping to the encoder it is written with, as r's own
// JSON would.
func (t textResult) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(t.r); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// LogValue is what r logs: its own LogValue, for what it measures rather than
// logs.
func (t textResult) LogValue() slog.Value {
	if v, ok := t.r.(slog.LogValuer); ok {
		return v.LogValue()
	}
	return slog.AnyValue(t.r)
}

func printResult(cmd *cobra.Command, r result) error {
	return clikit.PrintResult(cmd, textResult{r})
}

// invocation is the run cmd is part of as the cli's commands read it: a copy
// of its Env, and the Env itself for the client clientOf caches.
type invocation struct {
	*Env
	env     Env
	debugID string
	quiet   bool
}

func invocationOf(cmd *cobra.Command) invocation {
	env := envOf(cmd)
	inv := clikit.InvocationOf(cmd)
	return invocation{Env: env, env: *env, debugID: inv.DebugID, quiet: inv.Quiet}
}

func examples(pairs ...string) string { return clikit.Examples(pairs...) }

func annotation(items ...string) string { return clikit.Annotation(items...) }

func codeList[C ~string](codes ...C) string { return clikit.CodeList(codes...) }

func completeFlag(cmd *cobra.Command, name string, complete cobra.CompletionFunc) {
	clikit.CompleteFlag(cmd, name, complete)
}

func completeFiles(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return clikit.CompleteFiles(cmd, args, toComplete)
}

func completeDirs(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return clikit.CompleteDirs(cmd, args, toComplete)
}

func completeSettings(fileOnly bool) cobra.CompletionFunc {
	return clikit.CompleteSettings(config.Settings(), fileOnly)
}

func noArgs(cmd *cobra.Command, args []string) error { return clikit.NoArgs(cmd, args) }

func logAnswer(cmd *cobra.Command, question, answer, by string) {
	clikit.LogAnswer(cmd, question, answer, by)
}

func logUnanswered(cmd *cobra.Command, question string) { clikit.LogUnanswered(cmd, question) }

func canPrompt(cmd *cobra.Command) bool { return clikit.CanPrompt(cmd) }

func noPromptReason(cmd *cobra.Command) string { return clikit.NoPromptReason(cmd) }

func ask(prompt io.Writer, answers *bufio.Scanner, question string, choices ...string) (string, error) {
	return clikit.Ask(prompt, answers, question, choices...)
}

func absolute(cmd *cobra.Command, path string) (string, error) { return clikit.Absolute(cmd, path) }

func jsonMode(cmd *cobra.Command) bool { return clikit.JSONMode(cmd) }

func stderrStyle(cmd *cobra.Command) style.Style { return clikit.StderrStyle(cmd) }

func colorMode(cmd *cobra.Command, _ Env) style.ColorMode { return clikit.ColorMode(cmd) }

func fieldPrinter(w io.Writer, st style.Style, width int) func(name, value string) {
	return clikit.FieldPrinter(w, st, width)
}

func label(i int, name string) string { return clikit.Label(i, name) }

func usageText(st style.Style, line string) string { return clikit.UsageText(st, line) }

func hintText(st style.Style, hint string) string { return clikit.HintText(st, identity.Name, hint) }
