package aca

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"
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

// statusLineInput is what ContextSize and Usage read of a status line's input.
type statusLineInput struct {
	ContextWindow *struct {
		TotalInputTokens  int64 `json:"total_input_tokens"`
		ContextWindowSize int64 `json:"context_window_size"`
	} `json:"context_window"`
	// RateLimits is by window: whatever windows the ACA gives, which may grow.
	RateLimits map[string]struct {
		UsedPercentage float64 `json:"used_percentage"`
		ResetsAt       int64   `json:"resets_at"` // unix seconds; 0 for none
	} `json:"rate_limits"`
	// Quota is Antigravity's, by bucket id: whatever ids the account has, which may
	// grow. RemainingFraction is a pointer, as Antigravity leaves out a fraction of 0.
	Quota map[string]struct {
		RemainingFraction *float64 `json:"remaining_fraction"` // 0 to 1
		ResetTime         string   `json:"reset_time"`         // RFC 3339; "" for none
	} `json:"quota"`
}

// parseStatusLine is the file name in dir as a status line last wrote it. No file is
// os.ErrNotExist; one that does not parse an error naming it.
func parseStatusLine(dir, name string) (statusLineInput, error) {
	var input statusLineInput
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return input, err
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return input, fmt.Errorf("read %s: %w", name, err)
	}
	return input, nil
}

// readStatusUsage is the account's usage a status line last wrote to the file name in
// dir: a row per window of rate_limits, in the order of their keys, observed when the
// file was written. No file, or no rate_limits in it, is ErrNoUsage; a file that does
// not parse an error.
func readStatusUsage(dir, name string) ([]Usage, error) {
	input, err := parseStatusLine(dir, name)
	if errors.Is(err, os.ErrNotExist) || err == nil && len(input.RateLimits) == 0 {
		return nil, ErrNoUsage
	}
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	rows := make([]Usage, 0, len(input.RateLimits))
	for _, window := range slices.Sorted(maps.Keys(input.RateLimits)) {
		limit := input.RateLimits[window]
		row := Usage{Window: window, UsedPercent: int(math.Round(limit.UsedPercentage)), ObservedAt: info.ModTime()}
		if limit.ResetsAt != 0 {
			row.ResetsAt = time.Unix(limit.ResetsAt, 0)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// readStatusLine is the context size a status line last wrote to the file name in
// dir: the tokens in the context and the model's window. No file, or none of the
// context in it, is ErrNoContextSize; a file that does not parse an error. A null
// count or window is 0: Cursor writes both null before the first message and after
// /clear, where Claude Code writes 0 tokens and the window.
func readStatusLine(dir, name string) (ContextSize, error) {
	input, err := parseStatusLine(dir, name)
	if errors.Is(err, os.ErrNotExist) {
		return ContextSize{}, ErrNoContextSize
	}
	if err != nil {
		return ContextSize{}, err
	}
	if input.ContextWindow == nil {
		return ContextSize{}, ErrNoContextSize
	}
	return ContextSize{Tokens: input.ContextWindow.TotalInputTokens, Window: input.ContextWindow.ContextWindowSize}, nil
}
