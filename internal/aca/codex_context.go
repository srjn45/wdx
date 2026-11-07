package aca

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// codexTokenCount is in every token_count line of a session file, which Codex writes
// after each model response, so only such lines are parsed.
var codexTokenCount = []byte(`"type":"token_count"`)

// codexReadBlock is how much of a session file ContextSize reads at a time, from its
// end: the file can be several MB, and the last token_count line is near the end.
const codexReadBlock = 64 << 10

// codexTokenCountLine is what ContextSize reads of a token_count line. Its info is
// null on the first line of a session, which says nothing.
type codexTokenCountLine struct {
	Payload struct {
		Type string `json:"type"`
		Info *struct {
			LastTokenUsage struct {
				TotalTokens int64 `json:"total_tokens"`
			} `json:"last_token_usage"`
			ModelContextWindow int64 `json:"model_context_window"`
		} `json:"info"`
	} `json:"payload"`
}

// ContextSize implements AiCodingAgent, from the agent's session file, which Codex
// writes as a turn goes: the last token_count line whose info is not null. Its
// last_token_usage.total_tokens is the last request, input (cached included) and
// output, which is what is in the context; total_token_usage adds up every request
// of the session. After a compaction it is the request that wrote the summary, until
// the next response. The file is the one to follow; none is known before the first
// turn, when Codex has named the session and the backup found it. Checked on 0.163.0
// (the wdx doc CODEX_CONTEXT).
func (Codex) ContextSize(l Launch) (ContextSize, []string, error) {
	file, err := codexSessionPath(l)
	if err != nil {
		return ContextSize{}, nil, err
	}
	if file == "" {
		return ContextSize{}, nil, ErrNoContextSize
	}
	size, found, err := codexLastTokenCount(file)
	if errors.Is(err, os.ErrNotExist) {
		return ContextSize{}, nil, ErrNoContextSize
	}
	if err != nil {
		return ContextSize{}, []string{file}, err
	}
	if !found {
		return ContextSize{}, []string{file}, ErrNoContextSize
	}
	return size, []string{file}, nil
}

// codexSessionPath is where Codex keeps the session l.SessionID: where the backup's
// paths file says, else where BackUp's glob finds it; "" when neither knows.
func codexSessionPath(l Launch) (string, error) {
	if l.BackupDir != "" {
		paths, err := codexReadPaths(l.BackupDir)
		if err != nil {
			return "", err
		}
		if path := paths[l.SessionID]; path != "" {
			return path, nil
		}
	}
	return codexSessionFile(l.UserHome, l.SessionID)
}

// codexLastTokenCount reads the session file from its end, a block at a time, back to
// the last token_count line whose info is not null, and says whether there is one. A
// file shorter than it was when opened is read again at the next change.
func codexLastTokenCount(path string) (ContextSize, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return ContextSize{}, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ContextSize{}, false, err
	}
	// buf is what is read of the file, from pos to its end, that is not yet looked at:
	// a line is whole once the byte before it is read, or it starts the file.
	pos := info.Size()
	var buf []byte
	for {
		block := make([]byte, min(codexReadBlock, pos))
		if _, err := f.ReadAt(block, pos-int64(len(block))); err != nil {
			if errors.Is(err, io.EOF) {
				return ContextSize{}, false, nil
			}
			return ContextSize{}, false, err
		}
		pos -= int64(len(block))
		buf = append(block, buf...)
		whole := buf
		if pos > 0 {
			nl := bytes.IndexByte(buf, '\n')
			if nl < 0 {
				continue // one line longer than what is read: read more of it
			}
			whole = buf[nl+1:]
		}
		buf = buf[:len(buf)-len(whole)]
		for len(whole) > 0 {
			end := len(whole)
			if whole[end-1] == '\n' {
				end--
			}
			start := bytes.LastIndexByte(whole[:end], '\n') + 1
			if size, ok := codexParseTokenCount(whole[start:end]); ok {
				return size, true, nil
			}
			whole = whole[:start]
		}
		if pos == 0 {
			return ContextSize{}, false, nil
		}
	}
}

// codexParseTokenCount is the size a session line tells, when it is a token_count
// line whose info is not null. A line cut short, still being written, tells nothing.
func codexParseTokenCount(line []byte) (ContextSize, bool) {
	if !bytes.Contains(line, codexTokenCount) {
		return ContextSize{}, false
	}
	var entry codexTokenCountLine
	if json.Unmarshal(line, &entry) != nil || entry.Payload.Type != "token_count" || entry.Payload.Info == nil {
		return ContextSize{}, false
	}
	return ContextSize{Tokens: entry.Payload.Info.LastTokenUsage.TotalTokens, Window: entry.Payload.Info.ModelContextWindow}, true
}
