package aca

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Codex names its own sessions, so wdx learns which one an agent is in from Codex's
// notify program, which it runs after every turn with the turn's thread-id: Command
// has it append each to codexTurnsFile in the agent's backup. A SessionStart hook would
// stop each new agent at Codex's prompt to trust it. The wdx doc CODEX_RESUME.
const codexTurnsFile = "codex-turns.jsonl"

// sessionUUID is the form of the ids Codex and Antigravity give their sessions,
// which wdx's own also have; it keeps one from being a pattern when looked for.
var sessionUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// codexNotify is the -c that has Codex append each turn's JSON, its last argument, to
// the turns file in backupDir.
func codexNotify(backupDir string) string {
	log := filepath.Join(backupDir, codexTurnsFile)
	words := []string{"sh", "-c", `printf '%s\n' "$1" >> ` + shellQuote(log), "wdx"}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = tomlString(w)
	}
	return "notify=[" + strings.Join(quoted, ",") + "]"
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// codexSessionFile is the file Codex keeps the session id in under home, or "" when it
// keeps none: id is wdx's own until Codex's first turn, or Codex deleted it.
func codexSessionFile(home, id string) (string, error) {
	if !sessionUUID.MatchString(id) {
		return "", nil
	}
	files, err := filepath.Glob(filepath.Join(home, ".codex", "sessions", "*", "*", "*", "rollout-*-"+id+".jsonl"))
	if err != nil || len(files) == 0 {
		return "", err
	}
	return files[0], nil
}

// BackUp implements AiCodingAgent: it copies what is new of the user's sessions and
// their subagents' to the backup, appends what is new of the sessions to
// TranscriptFile as text, and returns the last thread of the turns file that is the
// user's conversation, not the thread Codex names it in nor a subagent's, and the
// directory Codex last worked in, where a resume asks nothing. A thread whose file
// Codex deleted is read from the backup's copy, which Command puts back, when the
// backup knows where it was. After a restart only a thread that came after it counts:
// until one does, the agent is in the restart's session, which a start begins.
func (Codex) BackUp(ctx context.Context, l Launch) (Backup, error) {
	first, last, err := codexThreads(filepath.Join(l.BackupDir, codexTurnsFile))
	if err != nil {
		return Backup{}, err
	}
	restarts, err := codexReadRestarts(l.BackupDir)
	if err != nil {
		return Backup{}, err
	}
	replaced, copied := codexCopySessions(ctx, l, first)
	failed := errors.Join(copied, codexRenderSessions(ctx, l.BackupDir, first, replaced))
	since := 0
	if len(restarts) > 0 {
		since = restarts[len(restarts)-1].Threads
	}
	for _, id := range slices.Backward(last) {
		if slices.Index(first, id) < since {
			continue
		}
		file, err := codexSessionFile(l.UserHome, id)
		if err != nil {
			return Backup{}, errors.Join(failed, err)
		}
		if file == "" {
			// Gone from Codex, its copy is the session, as Command puts it back.
			if file, _, err = codexKept(l.BackupDir, id); err != nil {
				return Backup{}, errors.Join(failed, err)
			}
		}
		if file == "" {
			continue
		}
		user, dir, err := codexReadSession(file)
		if err != nil {
			return Backup{}, errors.Join(failed, err)
		}
		if user {
			return Backup{Session: id, Dir: dir}, failed
		}
	}
	if len(restarts) > 0 {
		return Backup{Session: restarts[len(restarts)-1].Session}, failed
	}
	return Backup{}, failed
}

// codexThreads is the thread-ids of the turns file, each once, in the order they
// first came and in the order they last came. A line a killed notify cut short is
// left out.
func codexThreads(path string) (first, last []string, err error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		var turn struct {
			ThreadID string `json:"thread-id"`
		}
		if json.Unmarshal(line, &turn) == nil && turn.ThreadID != "" {
			if !slices.Contains(first, turn.ThreadID) {
				first = append(first, turn.ThreadID)
			}
			last = slices.DeleteFunc(last, func(id string) bool { return id == turn.ThreadID })
			last = append(last, turn.ThreadID)
		}
		if errors.Is(err, io.EOF) {
			return first, last, nil
		}
		if err != nil {
			return nil, nil, err
		}
	}
}

// codexReadSession says whether the session file is a conversation the user had, and
// the cwd of its last turn, or of the session when no turn says one. Lines are read
// one at a time, and only those two kinds parsed: a session can grow large.
func codexReadSession(path string) (user bool, dir string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return false, "", err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for first := true; ; first = false {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			kind := codexLineType(line)
			if first && kind != "session_meta" {
				return false, "", fmt.Errorf("session file %s does not start with its session_meta", path)
			}
			if kind == "session_meta" || kind == "turn_context" {
				var entry struct {
					Payload struct {
						Cwd          string `json:"cwd"`
						ThreadSource string `json:"thread_source"`
					} `json:"payload"`
				}
				if json.Unmarshal(line, &entry) == nil {
					if first {
						user = entry.Payload.ThreadSource == "user"
					}
					dir = cmp.Or(entry.Payload.Cwd, dir)
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return user, dir, nil
		}
		if err != nil {
			return false, "", err
		}
	}
}

// codexLineType is a session line's type, which Codex writes before its payload, so
// only the line's start is looked at.
func codexLineType(line []byte) string {
	head := line[:min(len(line), 200)]
	_, rest, ok := bytes.Cut(head, []byte(`"type":"`))
	if !ok {
		return ""
	}
	kind, _, _ := bytes.Cut(rest, []byte(`"`))
	return string(kind)
}
