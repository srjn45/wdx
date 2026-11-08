package clikit

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// CanPrompt reports whether cmd may ask the user questions: stdin is a
// terminal and neither --json nor --no-input (which the no_input setting
// defaults) is set.
func CanPrompt(cmd *cobra.Command) bool {
	return NoPromptReason(cmd) == ""
}

// NoPromptReason says why cmd may not ask questions; "" when it may.
func NoPromptReason(cmd *cobra.Command) string {
	noInput, _ := cmd.Flags().GetBool("no-input")
	switch {
	case noInput:
		return "--no-input"
	case JSONMode(cmd):
		return "--json"
	case !IsTerminal(cmd.InOrStdin()):
		return "stdin is not a terminal"
	}
	return ""
}

// IsTerminal reports whether stream, a reader or a writer, is a terminal.
func IsTerminal(stream any) bool {
	f, ok := stream.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// Ask prompts until one of choices is given; with no choices any answer,
// even an empty one, is accepted.
func Ask(prompt io.Writer, answers *bufio.Scanner, question string, choices ...string) (string, error) {
	for {
		fmt.Fprint(prompt, question)
		if !answers.Scan() {
			return "", cmp.Or(answers.Err(), errors.New("no answer given"))
		}
		answer := strings.TrimSpace(answers.Text())
		if len(choices) == 0 {
			return answer, nil
		}
		if answer = strings.ToLower(answer); slices.Contains(choices, answer) {
			return answer, nil
		}
	}
}
