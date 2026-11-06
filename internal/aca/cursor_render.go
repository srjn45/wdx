package aca

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/logging"
)

// cursorRenderedFile is how far TranscriptFile has got in each of the agent's Cursor
// chats, and what Command needs to put one back: each ACA keeps its own, so that after
// a switch they do not overwrite each other. The wdx doc CURSOR_PARITY.
const cursorRenderedFile = "cursor-rendered.json"

// cursorRendered is how far TranscriptFile has got.
type cursorRendered struct {
	Chats map[string]*cursorRenderedChat `json:"chats"`
	// Session and Heading are what the text last wrote under: a chat goes on under the
	// same headings, unless another came between.
	Session string `json:"session"`
	Heading string `json:"heading"`
}

// cursorRenderedChat is one chat's part of cursorRendered.
type cursorRenderedChat struct {
	// Dir is where the chat was made, where it must be resumed.
	Dir string `json:"dir"`
	// Rendered is the ids of its messages in the text. A message is rendered once,
	// and stays in the text when it leaves the context, by a rewind or a compaction.
	Rendered []string `json:"rendered"`
	// At is the time its messages are rendered with, the latest a request of it
	// carried, as Cursor keeps no time for a message.
	At string `json:"at,omitempty"`
	// Snapshot is the root its snapshot was taken at.
	Snapshot string `json:"snapshot,omitempty"`
	rendered map[string]bool
}

func readCursorRendered(backup string) (*cursorRendered, error) {
	state := &cursorRendered{Chats: map[string]*cursorRenderedChat{}}
	data, err := os.ReadFile(filepath.Join(backup, cursorRenderedFile))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("read %s: %w", cursorRenderedFile, err)
	}
	if state.Chats == nil {
		state.Chats = map[string]*cursorRenderedChat{}
	}
	for _, chat := range state.Chats {
		chat.rendered = map[string]bool{}
		for _, id := range chat.Rendered {
			chat.rendered[id] = true
		}
	}
	return state, nil
}

func (r *cursorRendered) write(backup string) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(backup, cursorRenderedFile), data, 0o600)
}

// chat is the part of chat id, made in dir.
func (r *cursorRendered) chat(id, dir string) *cursorRenderedChat {
	chat := r.Chats[id]
	if chat == nil {
		chat = &cursorRenderedChat{Rendered: []string{}, rendered: map[string]bool{}}
		r.Chats[id] = chat
	}
	chat.Dir = dir
	return chat
}

// cursorBackUpChats brings the backup up to date with each chat, in the order they
// were last updated: appends to TranscriptFile its messages not yet there, and
// snapshots its store when it changed. A chat whose store cannot be read is logged
// and passed over, and the text keeps what it had.
func cursorBackUpChats(ctx context.Context, backup string, chats []cursorChat, now time.Time) error {
	state, err := readCursorRendered(backup)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(filepath.Join(backup, TranscriptFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	w := bufio.NewWriter(out)
	var failed []error
	for _, c := range chats {
		if err := state.backUpChat(ctx, w, backup, c, now); err != nil {
			logging.FromContext(ctx).Error("agent chat not backed up", "session_id", c.ID, "err", err)
			failed = append(failed, fmt.Errorf("chat %s: %w", c.ID, err))
		}
	}
	failed = append(failed, w.Flush())
	if err := state.write(backup); err != nil {
		failed = append(failed, err)
	}
	return errors.Join(failed...)
}

// backUpChat renders what is new in chat c and snapshots its store if it changed.
func (r *cursorRendered) backUpChat(ctx context.Context, w io.Writer, backup string, c cursorChat, now time.Time) error {
	store, err := openCursorStore(filepath.Join(c.Path, cursorStoreFile))
	if err != nil || store == nil {
		return err
	}
	defer store.Close()
	chat := r.chat(c.ID, c.Cwd)
	root, messages, err := store.messages(ctx, chat.rendered)
	if err != nil {
		return err
	}
	skipped := 0
	for _, m := range messages {
		if !r.renderMessage(w, c.ID, chat, m.Data, now) {
			skipped++
		}
		chat.rendered[m.ID] = true
		chat.Rendered = append(chat.Rendered, m.ID)
	}
	if skipped > 0 {
		logging.FromContext(ctx).Info("transcript messages not rendered", "session_id", c.ID, "skipped", skipped)
	}
	if root == chat.Snapshot {
		return nil
	}
	dir := filepath.Join(backup, cursorSnapshotsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dest := filepath.Join(dir, c.ID+".db")
	if err := store.snapshot(ctx, dest); err != nil {
		return fmt.Errorf("snapshot its store: %w", err)
	}
	chat.Snapshot = root
	logging.FromContext(ctx).Info("agent chat snapshot taken", "session_id", c.ID, "path", dest)
	return nil
}

// cursorMessage is a message of a chat's context, the fields of every role in one.
type cursorMessage struct {
	Role            string          `json:"role"`
	Content         json.RawMessage `json:"content"`
	ProviderOptions struct {
		Cursor struct {
			// RequestContextCompleteness is on the message of what Cursor tells the
			// model of the machine, the repository and the user, which is left out.
			RequestContextCompleteness json.RawMessage `json:"requestContextCompleteness"`
			HighLevelToolCallResult    struct {
				IsError bool `json:"isError"`
			} `json:"highLevelToolCallResult"`
		} `json:"cursor"`
	} `json:"providerOptions"`
}

// cursorPart is a part of a message's content.
type cursorPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	ToolName string          `json:"toolName"`
	Args     json.RawMessage `json:"args"`
	Result   json.RawMessage `json:"result"`
}

// What Cursor wraps a request in, and what it puts in a request of its own.
const (
	cursorSummaryPrefix      = "[Previous conversation summary]: "
	cursorSummaryLocation    = "\n### Transcript location:"
	cursorNotificationOpen   = "<system_notification>"
	cursorNotificationClose  = "</system_notification>"
	cursorUserInfo           = "<user_info>"
	cursorNotificationFormat = "(task finished: %s)"
)

var (
	cursorTimestamp = regexp.MustCompile(`<timestamp>([^<]*)</timestamp>`)
	cursorUserQuery = regexp.MustCompile(`(?s)<user_query>\n?(.*?)\n?</user_query>`)
	cursorTask      = regexp.MustCompile(`(?s)<task>\n?(.*?)\n?</task>`)
	// cursorTime is a request's time as Cursor writes it, as "Friday, Nov 6, 2026,
	// 10:02 AM (UTC+1)".
	cursorTime = regexp.MustCompile(`^\w+, (\w+ \d{1,2}, \d{4}, \d{1,2}:\d{2} [AP]M) \(UTC(?:([+-])(\d{1,2})(?::(\d{2}))?)?\)$`)
)

// renderMessage writes one message as text, false when it could not read it.
func (r *cursorRendered) renderMessage(w io.Writer, session string, chat *cursorRenderedChat, data []byte, now time.Time) bool {
	var m cursorMessage
	if json.Unmarshal(data, &m) != nil {
		return false
	}
	var parts []cursorPart
	var text string
	if json.Unmarshal(m.Content, &text) == nil {
		parts = []cursorPart{{Type: "text", Text: text}}
	} else if json.Unmarshal(m.Content, &parts) != nil {
		return false
	}
	heading := func(name string, again bool) {
		if r.Session != session {
			writeSessionHeading(w, session, CursorID)
			r.Session, r.Heading = session, ""
		}
		if again || r.Heading != name {
			if chat.At == "" {
				chat.At = now.UTC().Format(time.RFC3339)
			}
			fmt.Fprintf(w, "## %s · %s\n\n", name, chat.At)
			r.Heading = name
		}
	}
	switch m.Role {
	case "system":
		return true
	case "user":
		if m.ProviderOptions.Cursor.RequestContextCompleteness != nil {
			return true
		}
		return r.renderUser(w, chat, parts, heading)
	case "assistant":
		for _, p := range parts {
			switch p.Type {
			case "text":
				if p.Text != "" {
					heading("Assistant", false)
					fmt.Fprintf(w, "%s\n\n", ansi.Strip(p.Text))
				}
			case "reasoning":
				// Its text is empty and its signature encrypted, as seen.
				if p.Text != "" {
					heading("Assistant", false)
					fmt.Fprintf(w, "%s\n\n%s\n\n%s\n\n", transcriptThinkingHeading, ansi.Strip(p.Text), transcriptTextHeading)
				}
			case "tool-call":
				heading("Assistant", false)
				fmt.Fprintf(w, "### Tool: %s\n\n%s\n\n", ansi.Strip(p.ToolName), fenced(string(p.Args)))
			default:
				return false
			}
		}
		return true
	case "tool":
		for _, p := range parts {
			if p.Type != "tool-result" {
				return false
			}
			heading("Assistant", false)
			title := "Result"
			if m.ProviderOptions.Cursor.HighLevelToolCallResult.IsError {
				title = "Result (error)"
			}
			result := string(p.Result)
			var s string
			if json.Unmarshal(p.Result, &s) == nil {
				result = s
			}
			fmt.Fprintf(w, "### %s\n\n%s\n\n", title, fenced(result))
		}
		return true
	}
	return false
}

// renderUser writes a user message: a request, a compaction's summary or a task's
// end. The message telling the model of the machine is left out.
func (r *cursorRendered) renderUser(w io.Writer, chat *cursorRenderedChat, parts []cursorPart, heading func(string, bool)) bool {
	var texts []string
	images := 0
	for _, p := range parts {
		switch p.Type {
		case "text":
			texts = append(texts, p.Text)
		case "image", "file":
			images++
		default:
			return false
		}
	}
	text := strings.Join(texts, "\n\n")
	if strings.HasPrefix(text, cursorUserInfo) {
		return true
	}
	if m := cursorTimestamp.FindStringSubmatch(text); m != nil {
		chat.setAt(m[1])
	}
	if summary, ok := strings.CutPrefix(text, cursorSummaryPrefix); ok {
		summary, _, _ = strings.Cut(summary, cursorSummaryLocation)
		heading("Compacted", true)
		fmt.Fprintf(w, "%s\n\n", ansi.Strip(strings.TrimSpace(summary)))
		return true
	}
	if open := strings.Index(text, cursorNotificationOpen); open >= 0 {
		// Cursor's own request follows it, to report the task: not the user's words.
		notification := text[open+len(cursorNotificationOpen):]
		notification, _, _ = strings.Cut(notification, cursorNotificationClose)
		heading("Assistant", false)
		fmt.Fprintf(w, "%s\n\n", ansi.Strip(cursorTaskLine(notification)))
		return true
	}
	heading("User", true)
	query := text
	if m := cursorUserQuery.FindStringSubmatch(text); m != nil {
		query = m[1]
	}
	if query = strings.TrimSpace(query); query != "" {
		fmt.Fprintf(w, "%s\n\n", ansi.Strip(query))
	}
	for range images {
		fmt.Fprint(w, "[image]\n\n")
	}
	return true
}

// cursorTaskLine is one line for a task's end, from what its notification says of it,
// or the notification itself when it names no task.
func cursorTaskLine(notification string) string {
	m := cursorTask.FindStringSubmatch(notification)
	if m == nil {
		return strings.TrimSpace(notification)
	}
	fields := map[string]string{}
	for _, line := range strings.Split(m[1], "\n") {
		if key, value, ok := strings.Cut(line, ": "); ok {
			fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	var said []string
	for _, key := range []string{"title", "kind", "status"} {
		if fields[key] != "" {
			said = append(said, fields[key])
		}
	}
	return fmt.Sprintf(cursorNotificationFormat, strings.Join(said, " · "))
}

// setAt moves the chat's time to a request's, never back: a time it cannot read
// leaves it as it was.
func (c *cursorRenderedChat) setAt(timestamp string) {
	at, ok := parseCursorTime(timestamp)
	if !ok {
		return
	}
	if was, err := time.Parse(time.RFC3339, c.At); err == nil && at.Before(was) {
		return
	}
	c.At = at.Format(time.RFC3339)
}

// parseCursorTime reads a request's time as Cursor writes it, in the offset it names.
func parseCursorTime(s string) (time.Time, bool) {
	m := cursorTime.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return time.Time{}, false
	}
	offset := 0
	if m[2] != "" {
		hours, _ := strconv.Atoi(m[3])
		minutes, _ := strconv.Atoi(m[4])
		offset = hours*3600 + minutes*60
		if m[2] == "-" {
			offset = -offset
		}
	}
	at, err := time.ParseInLocation("Jan 2, 2006, 3:04 PM", m[1], time.FixedZone("", offset))
	return at, err == nil
}
