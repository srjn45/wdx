package aca

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// claudeCodeInputKept is as much of a tool's input as its short form looks at: a
	// Write's holds a whole file.
	claudeCodeInputKept = 16 << 10
	claudeCodeInputShow = 100
)

// claudeCodeInputKeys are what a tool's input is best told by, in order.
var claudeCodeInputKeys = []string{"command", "file_path", "path", "notebook_path", "pattern", "query", "url", "skill", "description", "prompt"}

// Brief implements AiCodingAgent, reading no further back than the latest /clear.
func (ClaudeCode) Brief(_ context.Context, l Launch, limit int) (Brief, error) {
	starts, err := readClaudeCodeStarts(l.BackupDir)
	if err != nil {
		return Brief{}, err
	}
	return readTranscriptBrief(l.BackupDir, claudeCodeSinceClear(starts), limit)
}

// claudeCodeSinceClear is the sessions from the latest /clear on, nil when there was
// none: the brief looks no further back. Those a restart began do not stop it. Which one
// was written first decides, as a /clear's session may have written nothing.
func claudeCodeSinceClear(starts []claudeCodeStart) map[string]bool {
	var since map[string]bool
	for _, s := range starts {
		if s.Source == claudeCodeSourceClear {
			since = map[string]bool{}
		}
		if since != nil {
			since[s.SessionID] = true
		}
	}
	return since
}

// claudeCodeShortInput is a tool's input in a few words, with a space before them.
func claudeCodeShortInput(input string) string {
	values := map[string]string{}
	var first string
	d := json.NewDecoder(strings.NewReader(input))
	if t, err := d.Token(); err != nil || t != json.Delim('{') {
		return ""
	}
	// By its tokens, as an input past claudeCodeInputKept is cut off: what comes first
	// is still read.
	for d.More() {
		key, err := d.Token()
		if err != nil {
			break
		}
		var value any
		if err := d.Decode(&value); err != nil {
			break
		}
		if text, ok := value.(string); ok {
			if first == "" {
				first = text
			}
			values[fmt.Sprint(key)] = text
		}
	}
	short := first
	for _, key := range claudeCodeInputKeys {
		if v, ok := values[key]; ok {
			short = v
			break
		}
	}
	short = strings.Join(strings.Fields(short), " ")
	if short == "" {
		return ""
	}
	if runes := []rune(short); len(runes) > claudeCodeInputShow {
		short = string(runes[:claudeCodeInputShow]) + "…"
	}
	return " " + short
}
