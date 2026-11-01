package aca

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/logging"
)

// What a Claude Code backup holds besides TranscriptFile. docs/AGENT_RESUME.md.
const (
	claudeCodeSessionsFile = "sessions.jsonl"
	claudeCodeSettingsFile = "claude-settings.json"
	claudeCodeRenderedFile = "rendered.json"
	claudeCodeRawDir       = "transcripts"
	claudeCodeTailCheck    = 4096
)

// claudeCodeWriteSettings writes the settings that have Claude Code append each
// session it starts to the backup, /clear's among them, and returns their path.
// Given with --settings, they add to the user's own hooks.
func claudeCodeWriteSettings(backup string) (string, error) {
	log := "'" + strings.ReplaceAll(filepath.Join(backup, claudeCodeSessionsFile), "'", `'\''`) + "'"
	settings := map[string]any{"hooks": map[string]any{"SessionStart": []any{map[string]any{
		"matcher": "",
		"hooks":   []any{map[string]any{"type": "command", "command": "cat >> " + log + "; echo >> " + log}},
	}}}}
	data, err := json.Marshal(settings)
	if err != nil {
		return "", err
	}
	path := filepath.Join(backup, claudeCodeSettingsFile)
	if err := os.MkdirAll(backup, 0o700); err != nil {
		return "", err
	}
	return path, atomicfile.Write(path, data, 0o600)
}

// claudeCodeSession is one session of an agent, and where Claude Code keeps it.
type claudeCodeSession struct {
	ID         string
	Transcript string
}

// claudeCodeSessions returns the agent's sessions, oldest first, and the one it
// is in now. Before its first launch with the hook it has only l.SessionID.
func claudeCodeSessions(l Launch) (sessions []claudeCodeSession, current string, err error) {
	current = l.SessionID
	index := map[string]int{}
	add := func(id, transcript string) {
		if i, ok := index[id]; ok {
			if transcript != "" {
				sessions[i].Transcript = transcript
			}
			return
		}
		index[id] = len(sessions)
		sessions = append(sessions, claudeCodeSession{ID: id, Transcript: transcript})
	}
	add(l.SessionID, "")
	f, err := os.Open(filepath.Join(l.BackupDir, claudeCodeSessionsFile))
	if err == nil {
		defer f.Close()
		// A decoder, as a hook still writing leaves half a value at the end.
		for d := json.NewDecoder(f); ; {
			var started struct {
				SessionID  string `json:"session_id"`
				Transcript string `json:"transcript_path"`
			}
			if d.Decode(&started) != nil {
				break
			}
			if started.SessionID == "" || filepath.Base(started.SessionID) != started.SessionID {
				continue
			}
			add(started.SessionID, started.Transcript)
			current = started.SessionID
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	for i, s := range sessions {
		if s.Transcript != "" {
			continue
		}
		found, err := filepath.Glob(filepath.Join(l.UserHome, ".claude", "projects", "*", s.ID+".jsonl"))
		if err != nil {
			return nil, "", err
		}
		if len(found) > 0 {
			sessions[i].Transcript = found[0]
		}
	}
	return sessions, current, nil
}

// claudeCodeTranscriptAt reports whether l.SessionID has a transcript where
// Claude Code looks, putting back the backup's when only that is left.
func claudeCodeTranscriptAt(l Launch) (bool, error) {
	sessions, _, err := claudeCodeSessions(l)
	if err != nil {
		return false, err
	}
	var at string
	for _, s := range sessions {
		if s.ID == l.SessionID {
			at = s.Transcript
		}
	}
	if at != "" {
		if _, err := os.Stat(at); err == nil {
			return true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	if l.BackupDir == "" {
		return false, nil
	}
	kept := filepath.Join(l.BackupDir, claudeCodeRawDir, l.SessionID+".jsonl")
	if _, err := os.Stat(kept); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if at == "" {
		at = filepath.Join(l.UserHome, ".claude", "projects", claudeCodeProjectDir(l.Dir), l.SessionID+".jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
		return false, err
	}
	if err := atomicfile.Copy(at, kept, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

var claudeCodeNotInDirName = regexp.MustCompile(`[^A-Za-z0-9]`)

// claudeCodeProjectDir names the directory of dir's transcripts. A name over 200
// characters is cut and hashed instead, which only the hook's path can tell.
func claudeCodeProjectDir(dir string) string {
	return claudeCodeNotInDirName.ReplaceAllString(dir, "-")
}

// BackUp implements AiCodingAgent: it copies what is new in the transcripts of
// the agent's sessions, with their subagents and spilled tool results, and
// appends it to TranscriptFile as text.
func (ClaudeCode) BackUp(ctx context.Context, l Launch) (string, error) {
	sessions, current, err := claudeCodeSessions(l)
	if err != nil {
		return l.SessionID, err
	}
	raw := filepath.Join(l.BackupDir, claudeCodeRawDir)
	if err := os.MkdirAll(raw, 0o700); err != nil {
		return current, err
	}
	state, err := readClaudeCodeRendered(l.BackupDir)
	if err != nil {
		return current, err
	}
	var failed []error
	for _, s := range sessions {
		dest := filepath.Join(raw, s.ID+".jsonl")
		if err := claudeCodeCopySession(ctx, s, dest, state); err != nil {
			failed = append(failed, fmt.Errorf("session %s: %w", s.ID, err))
			continue
		}
		if err := state.render(ctx, s.ID, dest, filepath.Join(l.BackupDir, TranscriptFile)); err != nil {
			failed = append(failed, fmt.Errorf("session %s: %w", s.ID, err))
		}
	}
	if err := state.write(l.BackupDir); err != nil {
		failed = append(failed, err)
	}
	return current, errors.Join(failed...)
}

// claudeCodeCopySession copies what is new of session s to dest and what lies in
// its directory beside it. A transcript that is gone leaves the copy as it is.
func claudeCodeCopySession(ctx context.Context, s claudeCodeSession, dest string, state *claudeCodeRendered) error {
	if s.Transcript == "" {
		return nil
	}
	replaced, err := copyNewBytes(ctx, s.Transcript, dest)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if replaced {
		delete(state.Offsets, s.ID)
	}
	source := strings.TrimSuffix(s.Transcript, ".jsonl")
	target := strings.TrimSuffix(dest, ".jsonl")
	err = filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		to := filepath.Join(target, rel)
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return err
		}
		_, err = copyNewBytes(ctx, path, to)
		return err
	})
	return err
}

// copyNewBytes appends to dest what src has past dest's size. A src that is not
// dest with more at its end was replaced: dest is set aside, never overwritten,
// and src is copied whole.
func copyNewBytes(ctx context.Context, src, dest string) (replaced bool, err error) {
	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return false, err
	}
	var have int64
	if kept, err := os.Stat(dest); err == nil {
		have = kept.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if have > 0 {
		same, err := sameTail(in, dest, have, info.Size())
		if err != nil {
			return false, err
		}
		if !same {
			aside := fmt.Sprintf("%s.replaced-%d", dest, time.Now().UnixMilli())
			if err := os.Rename(dest, aside); err != nil {
				return false, err
			}
			logging.FromContext(ctx).Info("transcript replaced, keeping the old one", "path", aside)
			replaced, have = true, 0
		}
	}
	if have == info.Size() {
		return replaced, nil
	}
	if _, err := in.Seek(have, io.SeekStart); err != nil {
		return replaced, err
	}
	out, err := os.OpenFile(dest, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return replaced, err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return replaced, err
	}
	return replaced, out.Close()
}

// sameTail reports whether in holds at least have bytes, ending as dest does.
func sameTail(in *os.File, dest string, have, size int64) (bool, error) {
	if size < have {
		return false, nil
	}
	n := min(have, claudeCodeTailCheck)
	want := make([]byte, n)
	f, err := os.Open(dest)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if _, err := f.ReadAt(want, have-n); err != nil {
		return false, err
	}
	got := make([]byte, n)
	if _, err := in.ReadAt(got, have-n); err != nil {
		return false, err
	}
	return bytes.Equal(got, want), nil
}

// claudeCodeRendered is how far TranscriptFile has got.
type claudeCodeRendered struct {
	Offsets map[string]int64 `json:"offsets"`
	Session string           `json:"session"`
	Message string           `json:"message"`
}

func readClaudeCodeRendered(backup string) (*claudeCodeRendered, error) {
	state := &claudeCodeRendered{Offsets: map[string]int64{}}
	data, err := os.ReadFile(filepath.Join(backup, claudeCodeRenderedFile))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("read %s: %w", claudeCodeRenderedFile, err)
	}
	if state.Offsets == nil {
		state.Offsets = map[string]int64{}
	}
	return state, nil
}

func (r *claudeCodeRendered) write(backup string) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(backup, claudeCodeRenderedFile), data, 0o600)
}

// render appends to the text file the lines of the copy at raw past its offset.
// A line still being written waits for the next time.
func (r *claudeCodeRendered) render(ctx context.Context, session, raw, text string) error {
	in, err := os.Open(raw)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer in.Close()
	if _, err := in.Seek(r.Offsets[session], io.SeekStart); err != nil {
		return err
	}
	out, err := os.OpenFile(text, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	w := bufio.NewWriter(out)
	skipped := 0
	lines := bufio.NewReader(in)
	for {
		line, err := lines.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		r.Offsets[session] += int64(len(line))
		skipped += r.renderLine(w, session, line)
	}
	if skipped > 0 {
		logging.FromContext(ctx).Info("transcript lines not rendered", "session_id", session, "skipped", skipped)
	}
	return w.Flush()
}

// claudeCodeLine is a line of a transcript. Most lines are records of what
// Claude Code sent and did, which stay in the raw copy.
type claudeCodeLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Meta      bool   `json:"isMeta"`
	Sidechain bool   `json:"isSidechain"`
	Compacted bool   `json:"isCompactSummary"`
	Message   struct {
		ID      string          `json:"id"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type claudeCodeBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	Content  json.RawMessage `json:"content"`
	IsError  bool            `json:"is_error"`
}

// renderLine writes one line as text and returns how many parts of it it could
// not read.
func (r *claudeCodeRendered) renderLine(w io.Writer, session string, raw []byte) (skipped int) {
	var line claudeCodeLine
	if json.Unmarshal(raw, &line) != nil {
		return 1
	}
	if line.Type != "user" && line.Type != "assistant" || line.Meta || line.Sidechain {
		return 0
	}
	if r.Session != session {
		fmt.Fprintf(w, "# Session %s\n\n", session)
		r.Session, r.Message = session, ""
	}
	heading := func(name string) {
		key := name + line.Message.ID
		if line.Message.ID == "" {
			key += line.Timestamp
		}
		if r.Message != key {
			fmt.Fprintf(w, "## %s · %s\n\n", name, line.Timestamp)
			r.Message = key
		}
	}
	name := "User"
	switch {
	case line.Compacted:
		name = "Compacted"
	case line.Type == "assistant":
		name = "Assistant"
	}
	var text string
	if json.Unmarshal(line.Message.Content, &text) == nil {
		heading(name)
		fmt.Fprintf(w, "%s\n\n", ansi.Strip(text))
		return 0
	}
	var blocks []claudeCodeBlock
	if json.Unmarshal(line.Message.Content, &blocks) != nil {
		return 1
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			heading(name)
			fmt.Fprintf(w, "%s\n\n", ansi.Strip(b.Text))
		case "thinking":
			if b.Thinking != "" {
				heading(name)
				fmt.Fprintf(w, "### Thinking\n\n%s\n\n", ansi.Strip(b.Thinking))
			}
		case "tool_use":
			heading(name)
			fmt.Fprintf(w, "### Tool: %s\n\n%s\n\n", ansi.Strip(b.Name), fenced(string(b.Input)))
		case "tool_result":
			title := "Result"
			if b.IsError {
				title = "Result (error)"
			}
			fmt.Fprintf(w, "### %s\n\n%s\n\n", title, fenced(claudeCodeResultText(b.Content)))
		case "image":
			heading(name)
			fmt.Fprint(w, "[image]\n\n")
		default:
			skipped++
		}
	}
	return skipped
}

// claudeCodeResultText is a tool's result, a string or a list of blocks.
func claudeCodeResultText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []claudeCodeBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return string(raw)
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		} else {
			parts = append(parts, "["+b.Type+"]")
		}
	}
	return strings.Join(parts, "\n")
}

var backticks = regexp.MustCompile("`+")

// fenced is text in a code block whose fence nothing in the text can close.
func fenced(text string) string {
	fence := 3
	for _, run := range backticks.FindAllString(text, -1) {
		fence = max(fence, len(run)+1)
	}
	mark := strings.Repeat("`", fence)
	return mark + "\n" + ansi.Strip(text) + "\n" + mark
}
