package clikit

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/style"
)

// Result is what a command prints on success: the response as JSON, or as
// text styled by st.
type Result interface {
	Text(w io.Writer, st style.Style)
}

// PrintResult writes r to stdout as JSON or as text, and records it as the
// run's response.
func PrintResult(cmd *cobra.Command, r Result) error {
	InvocationOf(cmd).response = r
	if JSONMode(cmd) {
		return WriteJSON(cmd.OutOrStdout(), r)
	}
	r.Text(cmd.OutOrStdout(), StdoutStyle(cmd))
	return nil
}

// WriteJSON writes v as indented JSON.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// JSONMode reports whether --json was given.
func JSONMode(cmd *cobra.Command) bool {
	on, _ := cmd.Flags().GetBool("json")
	return on
}

// StdoutStyle styles what cmd prints to stdout.
func StdoutStyle(cmd *cobra.Command) style.Style {
	return style.New(colorOn(cmd, InvocationOf(cmd).Env, cmd.OutOrStdout()))
}

// StderrStyle styles what cmd prints to stderr.
func StderrStyle(cmd *cobra.Command) style.Style {
	return style.New(colorOn(cmd, InvocationOf(cmd).Env, cmd.ErrOrStderr()))
}

// colorOn reports whether cmd colors the text it writes to w: never with
// --json, else as --color or the color setting says.
func colorOn(cmd *cobra.Command, env Env, w io.Writer) bool {
	if JSONMode(cmd) {
		return false
	}
	switch ColorMode(cmd) {
	case style.ColorAlways:
		return true
	case style.ColorNever:
		return false
	}
	return !env.NoColor && IsTerminal(w)
}

// ColorMode is --color, which the color setting defaults; auto when cmd is
// in no tree NewRoot built.
func ColorMode(cmd *cobra.Command) style.ColorMode {
	if f := cmd.Root().PersistentFlags().Lookup("color"); f != nil {
		return style.ColorMode(f.Value.String())
	}
	return style.ColorAuto
}

// colorFlag is the value of --color.
type colorFlag struct {
	mode *style.ColorMode
}

func (f colorFlag) String() string { return string(*f.mode) }
func (f colorFlag) Type() string   { return "string" }

func (f colorFlag) Set(v string) (err error) {
	m, err := style.ParseColorMode(v)
	if err != nil {
		return err
	}
	*f.mode = m
	return nil
}

// FieldPrinter returns a func that writes an indented line of a name,
// padded to width, and its value.
func FieldPrinter(w io.Writer, st style.Style, width int) func(name, value string) {
	return func(name, value string) {
		fmt.Fprintf(w, "  %s %s\n", st.Muted(fmt.Sprintf("%-*s", width, name)), value)
	}
}

// Label is name on the first line of a list and blank on the others.
func Label(i int, name string) string {
	if i > 0 {
		return ""
	}
	return name
}
