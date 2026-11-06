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
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/logging"
)

// antigravityRenderedFile is how far TranscriptFile has got in each of the agent's
// Antigravity conversations, apart from the other ACAs'. The wdx doc
// ANTIGRAVITY_PARITY.
const antigravityRenderedFile = "antigravity-rendered.json"

// antigravityRendered is how far TranscriptFile has got.
type antigravityRendered struct {
	// Offsets is how far each conversation's copy has been read.
	Offsets map[string]int64 `json:"offsets"`
	// Steps is the step each conversation's text goes on from. A transcript
	// Antigravity started again, once its brain was deleted, goes on from the next
	// step, so a copy read again from its start skips what is in the text already.
	Steps map[string]int `json:"steps"`
	// Session is what the text last wrote under, and Assistant whether that was an
	// assistant's heading, which the turn's next step goes on under.
	Session   string `json:"session"`
	Assistant bool   `json:"assistant,omitempty"`
	// Calls is how many tool calls written wait for their result: Antigravity writes a
	// step for each, in the order of the calls.
	Calls int `json:"calls,omitempty"`
	// Thinking says thinking was the last thing written, whose end the text after it
	// has to mark.
	Thinking bool `json:"thinking,omitempty"`
}

func readAntigravityRendered(backup string) (*antigravityRendered, error) {
	state := &antigravityRendered{}
	data, err := os.ReadFile(filepath.Join(backup, antigravityRenderedFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(data, state); err != nil {
			return nil, fmt.Errorf("read %s: %w", antigravityRenderedFile, err)
		}
	}
	if state.Offsets == nil {
		state.Offsets = map[string]int64{}
	}
	if state.Steps == nil {
		state.Steps = map[string]int{}
	}
	return state, nil
}

func (r *antigravityRendered) write(backup string) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(backup, antigravityRenderedFile), data, 0o600)
}

// antigravityRenderConversations appends to TranscriptFile what is new in the copies of
// the conversations, in their order. One whose transcript was replaced is read again
// from its start.
func antigravityRenderConversations(ctx context.Context, backup string, conversations []string, replaced map[string]bool) error {
	state, err := readAntigravityRendered(backup)
	if err != nil {
		return err
	}
	var failed []error
	for _, id := range conversations {
		if replaced[id] {
			delete(state.Offsets, id)
		}
		raw := filepath.Join(backup, antigravityRawDir, id+".jsonl")
		if err := state.render(ctx, id, raw, filepath.Join(backup, TranscriptFile)); err != nil {
			failed = append(failed, fmt.Errorf("conversation %s: %w", id, err))
		}
	}
	if err := state.write(backup); err != nil {
		failed = append(failed, err)
	}
	return errors.Join(failed...)
}

// render appends to the text file the steps of the copy at raw past its offset, read a
// line at a time. A line still being written waits for the next time.
func (r *antigravityRendered) render(ctx context.Context, session, raw, text string) error {
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

// antigravityStep is a line of a transcript_full.jsonl, the fields of every type
// rendered in one.
type antigravityStep struct {
	Index     *int   `json:"step_index"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	Content   string `json:"content"`
	Thinking  string `json:"thinking"`
	Error     string `json:"error"`
	ToolCalls []struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"tool_calls"`
}

// renderLine writes the step of one line as text and returns how many it could not
// read: a line that is not JSON, or a step of a type it does not know.
func (r *antigravityRendered) renderLine(w io.Writer, session string, raw []byte) (skipped int) {
	var step antigravityStep
	if json.Unmarshal(raw, &step) != nil {
		return 1
	}
	if step.Index != nil {
		if *step.Index < r.Steps[session] {
			return 0
		}
		r.Steps[session] = *step.Index + 1
	}
	if !r.renderStep(w, session, step) {
		return 1
	}
	return 0
}

// renderStep writes step, false when its type is not one it knows.
func (r *antigravityRendered) renderStep(w io.Writer, session string, step antigravityStep) bool {
	heading := func(name string) {
		if r.Session != session {
			writeSessionHeading(w, session, AntigravityID)
			r.Session, r.Calls = session, 0
		}
		fmt.Fprintf(w, "## %s · %s\n\n", name, step.CreatedAt)
		r.Assistant, r.Thinking = name == "Assistant", false
	}
	assistant := func() {
		if r.Session != session || !r.Assistant {
			heading("Assistant")
		}
	}
	switch step.Type {
	case "USER_INPUT":
		heading("User")
		r.Calls = 0
		fmt.Fprintf(w, "%s\n\n", ansi.Strip(antigravityTagged(step.Content, "USER_REQUEST")))
	case "PLANNER_RESPONSE":
		assistant()
		if step.Thinking != "" {
			fmt.Fprintf(w, "%s\n\n%s\n\n", transcriptThinkingHeading, ansi.Strip(step.Thinking))
			r.Thinking = true
		}
		if step.Content != "" {
			if r.Thinking {
				fmt.Fprintf(w, "%s\n\n", transcriptTextHeading)
				r.Thinking = false
			}
			fmt.Fprintf(w, "%s\n\n", ansi.Strip(step.Content))
		}
		for _, call := range step.ToolCalls {
			fmt.Fprintf(w, "### Tool: %s\n\n%s\n\n", antigravityToolName(call.Name), fenced(string(call.Args)))
			r.Calls++
			r.Thinking = false
		}
	case "GENERIC":
		assistant()
		if r.Calls == 0 {
			// Antigravity's trimmed history can leave a result whose call it dropped.
			fmt.Fprintf(w, "### Tool: unknown\n\n%s\n\n", fenced(""))
		} else {
			r.Calls--
		}
		title, result := "Result", antigravityResult(step.Content)
		if step.Status == "ERROR" {
			title = "Result (error)"
			if result == "" {
				result = step.Error
			}
		}
		fmt.Fprintf(w, "### %s\n\n%s\n\n", title, fenced(result))
		r.Thinking = false
	case "CHECKPOINT":
		heading("Compacted")
		r.Calls = 0
		fmt.Fprintf(w, "%s\n\n", ansi.Strip(antigravitySummary(step.Content)))
	case "SYSTEM_MESSAGE":
		// A background task or a subagent reporting back, or a notice.
		heading("System")
		fmt.Fprintf(w, "%s\n\n", ansi.Strip(antigravityTagged(step.Content, "SYSTEM_MESSAGE")))
	case "ERROR_MESSAGE":
		// An error of Antigravity's API, which it retries: the human's decision 7.
	default:
		return false
	}
	return true
}

// antigravityToolName is a tool's name as one word, which a tool's heading takes.
func antigravityToolName(name string) string {
	name = strings.Join(strings.Fields(ansi.Strip(name)), "_")
	if name == "" {
		return "unknown"
	}
	return name
}

// antigravityTagged is the text between Antigravity's <tag> and </tag> lines, which
// it wraps what was sent in, leaving out what it adds after: the time, the settings
// changed. Text without them is taken whole.
func antigravityTagged(content, tag string) string {
	_, rest, ok := strings.Cut(content, "<"+tag+">\n")
	if !ok {
		return strings.TrimSpace(content)
	}
	text, _, _ := strings.Cut(rest, "\n</"+tag+">")
	return strings.TrimSpace(text)
}

// antigravityResult is a tool's result without the times Antigravity puts first, and
// with its terminals' line ends made plain.
func antigravityResult(content string) string {
	for {
		line, rest, _ := strings.Cut(content, "\n")
		if !strings.HasPrefix(line, "Created At: ") && !strings.HasPrefix(line, "Completed At: ") {
			break
		}
		content = rest
	}
	return strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n"))
}

// antigravitySummary is what of a compaction a person reads: the requests and the
// summary, without the instructions to the model around them, which name files of
// Antigravity's own.
func antigravitySummary(content string) string {
	if i := strings.Index(content, "# User Requests\n"); i >= 0 {
		content = content[i:]
	}
	if before, _, ok := strings.Cut(content, "\n# Conversation Logs\n"); ok {
		content = before
	}
	var kept []string
	for line := range strings.Lines(content) {
		if t := strings.TrimSpace(line); t != "<summary>" && t != "</summary>" {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, ""))
}
