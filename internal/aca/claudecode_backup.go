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
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
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
	// Where a session came from, in what is appended to claudeCodeSessionsFile.
	claudeCodeSourceClear   = "clear"
	claudeCodeSourceRestart = "wdx_restart"
	// claudeCodeSourceContinued marks a session found by a link in a transcript: Claude
	// Code moved the conversation to it, in a background process of its own.
	claudeCodeSourceContinued = "wdx_continued"
	// claudeCodeContinuedMark is how Claude Code itself looks for the link. A message
	// quoting it has its quotes escaped, and so does not match.
	claudeCodeContinuedMark = `"type":"continued-in"`
	claudeCodeCommandTime   = 30 * time.Second
)

// claudeCodeWriteSettings writes the settings that have Claude Code append each
// session it starts to the backup, /clear's among them, and keep what it gives its
// status line there, and returns their path. Given with --settings, they add to the
// user's own hooks. With PermissionsFull they also turn off the sandbox and the
// warning bypassing approvals starts with.
func claudeCodeWriteSettings(ctx context.Context, l Launch) (string, error) {
	log := shellQuote(filepath.Join(l.BackupDir, claudeCodeSessionsFile))
	settings := map[string]any{
		"hooks": map[string]any{"SessionStart": []any{map[string]any{
			"matcher": "",
			"hooks":   []any{map[string]any{"type": "command", "command": "cat >> " + log + "; echo >> " + log}},
		}}},
		"statusLine": map[string]any{"type": "command", "command": claudeCodeStatusLine(ctx, l)},
	}
	if l.Permissions == PermissionsFull {
		// The warning Claude Code shows the first time would stop the agent until a
		// human answers it, and settings given with --settings come before the user's.
		settings["skipDangerousModePermissionPrompt"] = true
		settings["sandbox"] = map[string]any{"enabled": false}
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return "", err
	}
	path := filepath.Join(l.BackupDir, claudeCodeSettingsFile)
	if err := os.MkdirAll(l.BackupDir, 0o700); err != nil {
		return "", err
	}
	return path, atomicfile.Write(path, data, 0o600)
}

// claudeCodeStatusLine is the status line's command, which Claude Code runs through a
// shell: it keeps what Claude Code gives it in claudeCodeStatusFile. The status line
// given with --settings replaces the user's own, so theirs then runs after it on the
// same input (checked on 2.1.319).
func claudeCodeStatusLine(ctx context.Context, l Launch) string {
	return statusLineCommand(filepath.Join(l.BackupDir, claudeCodeStatusFile), claudeCodeUserStatusLine(ctx, l.UserHome))
}

// claudeCodeUserStatusLine is the command of the user's own status line, in their
// ~/.claude/settings.json; "" when they have none. A project's own is not followed.
func claudeCodeUserStatusLine(ctx context.Context, home string) string {
	if home == "" {
		return ""
	}
	path := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	var settings struct {
		StatusLine struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"statusLine"`
	}
	if err == nil {
		err = json.Unmarshal(data, &settings)
	}
	if err != nil {
		logging.FromContext(ctx).Error("user's claude code status line not read", "path", path, "err", err)
		return ""
	}
	if settings.StatusLine.Type != "command" {
		return ""
	}
	return strings.TrimSpace(settings.StatusLine.Command)
}

// claudeCodeSession is one session of an agent, and where Claude Code keeps it.
type claudeCodeSession struct {
	ID         string
	Transcript string
	// ContinuedFrom is the session whose last messages this one's transcript starts with
	// copies of, if Claude Code moved the conversation here.
	ContinuedFrom string
}

// claudeCodeStart is a line of claudeCodeSessionsFile: a session the hook said started,
// or BeginSession began.
type claudeCodeStart struct {
	SessionID  string `json:"session_id"`
	Transcript string `json:"transcript_path,omitempty"`
	Source     string `json:"source,omitempty"`
	// ContinuedFrom is the session whose transcript links to this one. The link does not
	// make it the current session unless that one was current, as a link can be found
	// only after the agent has gone on to another.
	ContinuedFrom string `json:"continued_from,omitempty"`
}

// appendClaudeCodeStarts adds to claudeCodeSessionsFile, on lines of their own after
// whatever a killed hook left.
func appendClaudeCodeStarts(backup string, lines ...claudeCodeStart) error {
	var text bytes.Buffer
	path := filepath.Join(backup, claudeCodeSessionsFile)
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 && data[len(data)-1] != '\n' {
		text.WriteByte('\n')
	}
	for _, line := range lines {
		data, err := json.Marshal(line)
		if err != nil {
			return err
		}
		text.Write(data)
		text.WriteByte('\n')
	}
	if err := os.MkdirAll(backup, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(text.Bytes()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// readClaudeCodeStarts returns what claudeCodeSessionsFile holds, oldest first. A hook
// killed while writing leaves half a value, which is skipped to the end of its line, so
// that what was written after it is still read.
func readClaudeCodeStarts(backup string) ([]claudeCodeStart, error) {
	data, err := os.ReadFile(filepath.Join(backup, claudeCodeSessionsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var starts []claudeCodeStart
	for len(bytes.TrimSpace(data)) > 0 {
		var started claudeCodeStart
		d := json.NewDecoder(bytes.NewReader(data))
		if err := d.Decode(&started); err != nil {
			end := bytes.IndexByte(data, '\n')
			if end < 0 {
				break
			}
			data = data[end+1:]
			continue
		}
		data = data[d.InputOffset():]
		if started.SessionID != "" && filepath.Base(started.SessionID) == started.SessionID {
			starts = append(starts, started)
		}
	}
	return starts, nil
}

// claudeCodeDetachedID is what `claude agents` calls a background session. It goes into a
// command line for a shell that may be any shell, so it can hold nothing to quote.
var claudeCodeDetachedID = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// claudeCodeRun runs a command line of the claude program in the user's login shell.
func claudeCodeRun(ctx context.Context, l Launch, command string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, claudeCodeCommandTime)
	defer cancel()
	cmd := exec.CommandContext(ctx, l.Shell, "-lc", command)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", command, err)
	}
	return out, nil
}

// Detached implements AiCodingAgent: the sessions a transcript linked to that `claude
// agents` says have a process. It lists sessions of other work too, which are never
// touched, and ones with no process, which Claude Code remembers but which do not run.
func (ClaudeCode) Detached(ctx context.Context, l Launch) ([]DetachedSession, error) {
	starts, err := readClaudeCodeStarts(l.BackupDir)
	if err != nil {
		return nil, err
	}
	continued := map[string]bool{}
	for _, s := range starts {
		if s.ContinuedFrom != "" {
			continued[s.SessionID] = true
		}
	}
	if len(continued) == 0 {
		return nil, nil
	}
	out, err := claudeCodeRun(ctx, l, "claude agents --json")
	if err != nil {
		return nil, err
	}
	// A login shell's startup files can print before it.
	if at := bytes.IndexByte(out, '['); at > 0 {
		out = out[at:]
	}
	var running []struct {
		ID      string `json:"id"`
		Session string `json:"sessionId"`
		Kind    string `json:"kind"`
		PID     int    `json:"pid"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(out, &running); err != nil {
		return nil, fmt.Errorf("read claude agents: %w", err)
	}
	var detached []DetachedSession
	for _, r := range running {
		if r.Kind == "background" && r.PID != 0 && continued[r.Session] {
			detached = append(detached, DetachedSession{ID: r.ID, Session: r.Session, Working: r.Status != "idle"})
		}
	}
	return detached, nil
}

// StopDetached implements AiCodingAgent. `claude --resume` and `claude attach` still find
// the conversation.
func (ClaudeCode) StopDetached(ctx context.Context, l Launch, d DetachedSession) error {
	if !claudeCodeDetachedID.MatchString(d.ID) {
		return fmt.Errorf("not the id of a background session: %q", d.ID)
	}
	_, err := claudeCodeRun(ctx, l, "claude stop "+d.ID)
	return err
}

// BeginSession implements AiCodingAgent. Its line is what makes it the session BackUp
// says the agent is in, and what tells the brief a restart began it, not a /clear. It
// comes on a line of its own after whatever a killed hook left. An agent from before
// the hook has no line for the session it leaves, which would then go unfollowed.
// What the old session last gave its status line goes, or its context size would
// come back as the new session's.
func (ClaudeCode) BeginSession(_ context.Context, l Launch, session string) error {
	if err := os.Remove(filepath.Join(l.BackupDir, claudeCodeStatusFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	starts, err := readClaudeCodeStarts(l.BackupDir)
	if err != nil {
		return err
	}
	lines := []claudeCodeStart{{SessionID: session, Source: claudeCodeSourceRestart}}
	if !slices.ContainsFunc(starts, func(s claudeCodeStart) bool { return s.SessionID == l.SessionID }) {
		lines = append([]claudeCodeStart{{SessionID: l.SessionID}}, lines...)
	}
	return appendClaudeCodeStarts(l.BackupDir, lines...)
}

// claudeCodeSessions returns the agent's sessions, oldest first, and the one it
// is in now. Before its first launch with the hook it has only l.SessionID.
func claudeCodeSessions(l Launch) (sessions []claudeCodeSession, current string, err error) {
	current = l.SessionID
	index := map[string]int{}
	add := func(id, transcript, from string) {
		if i, ok := index[id]; ok {
			if transcript != "" {
				sessions[i].Transcript = transcript
			}
			if from != "" {
				sessions[i].ContinuedFrom = from
			}
			return
		}
		index[id] = len(sessions)
		sessions = append(sessions, claudeCodeSession{ID: id, Transcript: transcript, ContinuedFrom: from})
	}
	add(l.SessionID, "", "")
	starts, err := readClaudeCodeStarts(l.BackupDir)
	if err != nil {
		return nil, "", err
	}
	for _, started := range starts {
		if started.ContinuedFrom != "" {
			add(started.ContinuedFrom, "", "")
		}
		add(started.SessionID, started.Transcript, started.ContinuedFrom)
		if started.ContinuedFrom == "" || started.ContinuedFrom == current {
			current = started.SessionID
		}
	}
	for i, s := range sessions {
		if s.Transcript != "" {
			continue
		}
		if sessions[i].Transcript, err = claudeCodeFindTranscript(l, s.ID); err != nil {
			return nil, "", err
		}
	}
	return sessions, current, nil
}

// claudeCodeFindTranscript is where Claude Code keeps session id, "" when it keeps none.
func claudeCodeFindTranscript(l Launch, id string) (string, error) {
	found, err := filepath.Glob(filepath.Join(l.UserHome, ".claude", "projects", "*", id+".jsonl"))
	if err != nil || len(found) == 0 {
		return "", err
	}
	return found[0], nil
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
func (ClaudeCode) BackUp(ctx context.Context, l Launch) (Backup, error) {
	sessions, current, err := claudeCodeSessions(l)
	if err != nil {
		return Backup{Session: l.SessionID}, err
	}
	raw := filepath.Join(l.BackupDir, claudeCodeRawDir)
	if err := os.MkdirAll(raw, 0o700); err != nil {
		return Backup{Session: current}, err
	}
	state, err := readClaudeCodeRendered(l.BackupDir)
	if err != nil {
		return Backup{Session: current}, err
	}
	var failed []error
	// By index, as a session can link to one that joins the list here.
	for i := 0; i < len(sessions); i++ {
		s := sessions[i]
		dest := filepath.Join(raw, s.ID+".jsonl")
		if err := claudeCodeCopySession(ctx, s, dest, state); err != nil {
			failed = append(failed, fmt.Errorf("session %s: %w", s.ID, err))
			continue
		}
		linked, err := state.scanLinks(dest, s.ID)
		if err != nil {
			failed = append(failed, fmt.Errorf("session %s: %w", s.ID, err))
		}
		for _, next := range linked {
			if slices.ContainsFunc(sessions, func(known claudeCodeSession) bool { return known.ID == next }) {
				continue
			}
			transcript, err := claudeCodeFindTranscript(l, next)
			if err == nil {
				err = appendClaudeCodeStarts(l.BackupDir, claudeCodeStart{SessionID: next, Transcript: transcript, Source: claudeCodeSourceContinued, ContinuedFrom: s.ID})
			}
			if err != nil {
				failed = append(failed, fmt.Errorf("session %s: %w", next, err))
				continue
			}
			logging.FromContext(ctx).Info("agent session continued elsewhere", "session_id", s.ID, "to", next)
			sessions = append(sessions, claudeCodeSession{ID: next, Transcript: transcript, ContinuedFrom: s.ID})
			if current == s.ID {
				current = next
			}
		}
		if s.ContinuedFrom != "" && !state.PastCopy[s.ID] {
			if state.copied, err = claudeCodeUUIDs(filepath.Join(raw, s.ContinuedFrom+".jsonl")); err != nil {
				failed = append(failed, fmt.Errorf("session %s: %w", s.ContinuedFrom, err))
			}
		}
		state.past = s.ContinuedFrom != "" && s.ID != current
		if err := state.render(ctx, s.ID, dest, filepath.Join(l.BackupDir, TranscriptFile)); err != nil {
			failed = append(failed, fmt.Errorf("session %s: %w", s.ID, err))
		}
		state.copied = nil
	}
	if err := state.write(l.BackupDir); err != nil {
		failed = append(failed, err)
	}
	return Backup{Session: current, Dir: state.Dir}, errors.Join(failed...)
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
		delete(state.Scanned, s.ID)
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
	// Dir is where the agent last worked, which each line of a transcript says.
	Dir string `json:"dir,omitempty"`
	// Thinking says thinking was the last thing written, whose end the text after it
	// has to mark, thinking not being fenced.
	Thinking bool `json:"thinking,omitempty"`
	// Scanned is how far each session's copy has been looked through for links to the
	// session it went on in, apart from Offsets so that a backup from before the links
	// were followed still finds them.
	Scanned map[string]int64 `json:"scanned,omitempty"`
	// PastCopy holds the sessions rendered past what they copied from the one before.
	PastCopy map[string]bool `json:"past_copy,omitempty"`
	// copied is what the session being rendered may start with copies of, by uuid.
	copied map[string]bool
	// past says the session being rendered was found by a link only after the agent had
	// gone on, so that where it worked is not where the agent is.
	past bool
}

func readClaudeCodeRendered(backup string) (*claudeCodeRendered, error) {
	state := &claudeCodeRendered{Offsets: map[string]int64{}, Scanned: map[string]int64{}, PastCopy: map[string]bool{}}
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
	if state.Scanned == nil {
		state.Scanned = map[string]int64{}
	}
	if state.PastCopy == nil {
		state.PastCopy = map[string]bool{}
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

// scanLinks returns the sessions that the lines of the copy at raw, past how far it has
// been scanned, say session went on in. A line still being written waits for the next time.
func (r *claudeCodeRendered) scanLinks(raw, session string) ([]string, error) {
	in, err := os.Open(raw)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer in.Close()
	if _, err := in.Seek(r.Scanned[session], io.SeekStart); err != nil {
		return nil, err
	}
	var linked []string
	lines := bufio.NewReader(in)
	for {
		line, err := lines.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return linked, nil
		}
		if err != nil {
			return linked, err
		}
		r.Scanned[session] += int64(len(line))
		if !bytes.Contains(line, []byte(claudeCodeContinuedMark)) {
			continue
		}
		var link struct {
			Type        string `json:"type"`
			ContinuedIn string `json:"continuedInSessionId"`
		}
		if json.Unmarshal(line, &link) == nil && link.Type == "continued-in" && link.ContinuedIn != "" &&
			filepath.Base(link.ContinuedIn) == link.ContinuedIn {
			linked = append(linked, link.ContinuedIn)
		}
	}
}

// claudeCodeUUIDs is the uuid of each line of the copy at raw.
func claudeCodeUUIDs(raw string) (map[string]bool, error) {
	in, err := os.Open(raw)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer in.Close()
	uuids := map[string]bool{}
	lines := bufio.NewReader(in)
	for {
		line, err := lines.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return uuids, nil
		}
		if err != nil {
			return uuids, err
		}
		var with struct {
			UUID string `json:"uuid"`
		}
		if json.Unmarshal(line, &with) == nil && with.UUID != "" {
			uuids[with.UUID] = true
		}
	}
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
	UUID      string `json:"uuid"`
	Timestamp string `json:"timestamp"`
	Meta      bool   `json:"isMeta"`
	Sidechain bool   `json:"isSidechain"`
	Compacted bool   `json:"isCompactSummary"`
	Cwd       string `json:"cwd"`
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
	// A session Claude Code moved the conversation to starts with copies of the last
	// messages, up to the first line of its own, which are in the text already.
	if r.copied != nil && line.UUID != "" {
		if r.copied[line.UUID] {
			return 0
		}
		r.copied = nil
		r.PastCopy[session] = true
	}
	// A subagent's lines say where it works, which is not where the agent is.
	if line.Cwd != "" && !line.Sidechain && !r.past {
		r.Dir = line.Cwd
	}
	if line.Type != "user" && line.Type != "assistant" || line.Meta || line.Sidechain {
		return 0
	}
	if r.Session != session {
		writeSessionHeading(w, session, ClaudeCodeID)
		r.Session, r.Message = session, ""
	}
	heading := func(name string) {
		key := name + line.Message.ID
		if line.Message.ID == "" {
			key += line.Timestamp
		}
		if r.Message != key {
			fmt.Fprintf(w, "## %s · %s\n\n", name, line.Timestamp)
			r.Message, r.Thinking = key, false
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
			if r.Thinking {
				fmt.Fprintf(w, "%s\n\n", transcriptTextHeading)
				r.Thinking = false
			}
			fmt.Fprintf(w, "%s\n\n", ansi.Strip(b.Text))
		case "thinking":
			if b.Thinking != "" {
				heading(name)
				fmt.Fprintf(w, "%s\n\n%s\n\n", transcriptThinkingHeading, ansi.Strip(b.Thinking))
				r.Thinking = true
			}
		case "tool_use":
			heading(name)
			fmt.Fprintf(w, "### Tool: %s\n\n%s\n\n", ansi.Strip(b.Name), fenced(string(b.Input)))
			r.Thinking = false
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
