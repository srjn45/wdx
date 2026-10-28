package cli

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

// canPrompt reports whether cmd may ask the user questions: stdin is a
// terminal and neither --json nor --no-input (which the no_input setting defaults)
// is set.
func canPrompt(cmd *cobra.Command) bool {
	return noPromptReason(cmd) == ""
}

func noPromptReason(cmd *cobra.Command) string {
	noInput, _ := cmd.Flags().GetBool("no-input")
	switch {
	case noInput:
		return "--no-input"
	case jsonMode(cmd):
		return "--json"
	case !isTerminal(cmd.InOrStdin()):
		return "stdin is not a terminal"
	}
	return ""
}

// isTerminal reports whether stream, a reader or a writer, is a terminal.
func isTerminal(stream any) bool {
	f, ok := stream.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// ask prompts until one of choices is given; with no choices any answer,
// even an empty one, is accepted.
func ask(prompt io.Writer, answers *bufio.Scanner, question string, choices ...string) (string, error) {
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
