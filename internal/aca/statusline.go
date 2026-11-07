package aca

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// statusLineCommand is a status line's command for a shell: it writes what the ACA
// gives it to statusFile whole, through a file of its own renamed into place, so that
// a read never sees half of one and two runs at once do not mix. Then theirs, the
// user's own command, runs on the same input, in braces, which take any command a
// shell does; "" is none. Claude Code and Cursor give their status lines the same JSON.
func statusLineCommand(statusFile, theirs string) string {
	command := "f=" + shellQuote(statusFile) + `; cat > "$f.$$" && mv "$f.$$" "$f"`
	if theirs != "" {
		command += "; {\n" + theirs + "\n} < \"$f\""
	}
	return command
}

// statusLineInput is what ContextSize reads of a status line's input.
type statusLineInput struct {
	ContextWindow *struct {
		TotalInputTokens  int64 `json:"total_input_tokens"`
		ContextWindowSize int64 `json:"context_window_size"`
	} `json:"context_window"`
}

// readStatusLine is the context size a status line last wrote to the file name in
// dir: the tokens in the context and the model's window. No file, or none of the
// context in it, is ErrNoContextSize; a file that does not parse an error. A null
// count or window is 0: Cursor writes both null before the first message and after
// /clear, where Claude Code writes 0 tokens and the window.
func readStatusLine(dir, name string) (ContextSize, error) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return ContextSize{}, ErrNoContextSize
	}
	if err != nil {
		return ContextSize{}, err
	}
	var input statusLineInput
	if err := json.Unmarshal(data, &input); err != nil {
		return ContextSize{}, fmt.Errorf("read %s: %w", name, err)
	}
	if input.ContextWindow == nil {
		return ContextSize{}, ErrNoContextSize
	}
	return ContextSize{Tokens: input.ContextWindow.TotalInputTokens, Window: input.ContextWindow.ContextWindowSize}, nil
}
