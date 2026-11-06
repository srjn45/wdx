package aca

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// ACAs that name their own sessions cannot tell a session a restart began from one
// the user began (Codex's /new, Antigravity's /clear), so BeginSession records each
// restart with how many sessions there were by then: the first of the user's at or
// after that position, in the order they first came, is the restart's. The wdx doc
// CODEX_BRIEF.

// sessionsTheUserBegan is the user's sessions, of first, that the user began rather
// than a restart: every one after the first that is not the first of the user's at
// or after a position of restartAt. user says which of first are the user's.
func sessionsTheUserBegan(first []string, restartAt []int, user func(session string) bool) map[string]bool {
	restarted := map[string]bool{}
	for _, at := range restartAt {
		for _, id := range first[min(at, len(first)):] {
			if user(id) {
				restarted[id] = true
				break
			}
		}
	}
	began := map[string]bool{}
	seenFirst := false
	for _, id := range first {
		if !user(id) {
			continue
		}
		if seenFirst && !restarted[id] {
			began[id] = true
		}
		seenFirst = true
	}
	return began
}

// sinceTheLatest is the sessions of first from the latest of began on, the bound of a
// brief; nil, all of them, when began is empty.
func sinceTheLatest(first []string, began map[string]bool) map[string]bool {
	latest := len(first) - 1
	for latest >= 0 && !began[first[latest]] {
		latest--
	}
	if latest < 0 {
		return nil
	}
	since := map[string]bool{}
	for _, id := range first[latest:] {
		since[id] = true
	}
	return since
}

// appendJSONLine appends v as a line of JSON to the file name in dir.
func appendJSONLine(dir, name string, v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// readJSONLines is the lines of the file at path that read as T and keep says are
// whole, oldest first; none when there is no file. A line it cannot read is left out.
func readJSONLines[T any](path string, keep func(T) bool) ([]T, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var values []T
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		var v T
		if len(bytes.TrimSpace(line)) > 0 && json.Unmarshal(line, &v) == nil && keep(v) {
			values = append(values, v)
		}
		if errors.Is(err, io.EOF) {
			return values, nil
		}
		if err != nil {
			return nil, err
		}
	}
}
