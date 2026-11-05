package aca

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/logging"
)

// codexRenderedFile is how far TranscriptFile has got in each of the agent's Codex
// sessions: each ACA keeps its own, so that after a switch they do not overwrite each
// other. The wdx doc CODEX_BACKUP.
const codexRenderedFile = "codex-rendered.json"

// codexItemCompleted is in the line of a session file that holds an item Codex shows,
// the only lines rendered.
var codexItemCompleted = []byte(`"type":"item_completed"`)

// codexRendered is how far TranscriptFile has got.
type codexRendered struct {
	// Offsets is how far each session's copy has been rendered.
	Offsets map[string]int64 `json:"offsets"`
	// Session and Turn are what the text last wrote under: a resumed session goes on
	// under the same headings, unless another came between.
	Session string `json:"session"`
	Turn    string `json:"turn"`
	// thinking says thinking was the last thing written, whose end the text after it
	// has to mark, as in Claude Code's. It is not kept: a backup that stops between the
	// two leaves the text unmarked.
	thinking bool
	// rendered is the ids of the items of the session being rendered that are in the
	// text already, for this backup only: a session file that was replaced is
	// rendered again whole, as Claude Code's is.
	rendered map[string]bool
}

func readCodexRendered(backup string) (*codexRendered, error) {
	state := &codexRendered{Offsets: map[string]int64{}}
	data, err := os.ReadFile(filepath.Join(backup, codexRenderedFile))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("read %s: %w", codexRenderedFile, err)
	}
	if state.Offsets == nil {
		state.Offsets = map[string]int64{}
	}
	return state, nil
}

func (r *codexRendered) write(backup string) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(backup, codexRenderedFile), data, 0o600)
}

// codexRenderSessions appends to TranscriptFile what is new in the copies of the
// sessions, in their order. A session whose file was replaced is rendered again from
// its start, its items already in the text skipped.
func codexRenderSessions(ctx context.Context, backup string, sessions []string, replaced map[string]bool) error {
	state, err := readCodexRendered(backup)
	if err != nil {
		return err
	}
	var failed []error
	for _, id := range sessions {
		if replaced[id] {
			delete(state.Offsets, id)
		}
		raw := filepath.Join(backup, codexRawDir, id+".jsonl")
		if err := state.render(ctx, id, raw, filepath.Join(backup, TranscriptFile)); err != nil {
			failed = append(failed, fmt.Errorf("session %s: %w", id, err))
		}
	}
	if err := state.write(backup); err != nil {
		failed = append(failed, err)
	}
	return errors.Join(failed...)
}

// render appends to the text file the items of the copy at raw past its offset, read
// a line at a time. A line still being written waits for the next time.
func (r *codexRendered) render(ctx context.Context, session, raw, text string) error {
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
	r.rendered = map[string]bool{}
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

// codexLine is an item_completed line of a session file.
type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type   string          `json:"type"`
		TurnID string          `json:"turn_id"`
		Item   json.RawMessage `json:"item"`
	} `json:"payload"`
}

// codexItem is what an item_completed line says of the item, the fields of every kind
// rendered in one.
type codexItem struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	SummaryText      []string                   `json:"summary_text"`
	Command          []string                   `json:"command"`
	Cwd              string                     `json:"cwd"`
	AggregatedOutput string                     `json:"aggregated_output"`
	ExitCode         *int                       `json:"exit_code"`
	Status           string                     `json:"status"`
	Server           string                     `json:"server"`
	Tool             string                     `json:"tool"`
	Arguments        json.RawMessage            `json:"arguments"`
	Result           json.RawMessage            `json:"result"`
	Changes          map[string]codexFileChange `json:"changes"`
	Stdout           string                     `json:"stdout"`
	Stderr           string                     `json:"stderr"`
	Kind             string                     `json:"kind"`
	Text             string                     `json:"text"`
	Results          []struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	} `json:"results"`
}

type codexFileChange struct {
	Type        string  `json:"type"`
	Content     string  `json:"content"`
	UnifiedDiff string  `json:"unified_diff"`
	MovePath    *string `json:"move_path"`
}

// renderLine writes the item of one line as text and returns how many it could not
// read: a line that is not JSON, or an item of a kind it does not know.
func (r *codexRendered) renderLine(w io.Writer, session string, raw []byte) (skipped int) {
	if codexLineType(raw) == "" {
		return 1
	}
	if !bytes.Contains(raw, codexItemCompleted) {
		return 0
	}
	var line codexLine
	if json.Unmarshal(raw, &line) != nil {
		return 1
	}
	if line.Type != "event_msg" || line.Payload.Type != "item_completed" {
		return 0
	}
	var item codexItem
	if json.Unmarshal(line.Payload.Item, &item) != nil {
		return 1
	}
	if item.ID != "" && r.rendered[item.ID] {
		return 0
	}
	if !r.renderItem(w, session, line, item) {
		return 1
	}
	if item.ID != "" {
		r.rendered[item.ID] = true
	}
	return 0
}

// renderItem writes item, false when its kind is not one it knows.
func (r *codexRendered) renderItem(w io.Writer, session string, line codexLine, item codexItem) bool {
	heading := func(name string) {
		if r.Session != session {
			writeSessionHeading(w, session, CodexID)
			r.Session, r.Turn = session, ""
		}
		fmt.Fprintf(w, "## %s · %s\n\n", name, line.Timestamp)
		r.thinking = false
	}
	assistant := func() {
		if r.Session != session || r.Turn != line.Payload.TurnID {
			heading("Assistant")
			r.Turn = line.Payload.TurnID
		}
	}
	tool := func(name, input, result string, failed bool) {
		assistant()
		fmt.Fprintf(w, "### Tool: %s\n\n%s\n\n", ansi.Strip(name), fenced(input))
		title := "Result"
		if failed {
			title = "Result (error)"
		}
		fmt.Fprintf(w, "### %s\n\n%s\n\n", title, fenced(result))
		r.thinking = false
	}
	switch item.Type {
	case "UserMessage":
		heading("User")
		r.Turn = ""
		for _, c := range item.Content {
			if c.Type == "text" {
				fmt.Fprintf(w, "%s\n\n", ansi.Strip(c.Text))
			} else {
				fmt.Fprint(w, "[image]\n\n")
			}
		}
	case "AgentMessage":
		assistant()
		if r.thinking {
			fmt.Fprintf(w, "%s\n\n", transcriptTextHeading)
			r.thinking = false
		}
		for _, c := range item.Content {
			fmt.Fprintf(w, "%s\n\n", ansi.Strip(c.Text))
		}
	case "Reasoning":
		if summary := strings.Join(item.SummaryText, "\n\n"); summary != "" {
			assistant()
			fmt.Fprintf(w, "%s\n\n%s\n\n", transcriptThinkingHeading, ansi.Strip(summary))
			r.thinking = true
		}
	case "CommandExecution":
		input, _ := json.Marshal(struct {
			Command []string `json:"command"`
			Cwd     string   `json:"cwd,omitempty"`
		}{item.Command, strings.TrimPrefix(item.Cwd, "file://")})
		result := item.AggregatedOutput
		if item.ExitCode != nil {
			result = strings.TrimSuffix(result, "\n") + fmt.Sprintf("\nexit code %d", *item.ExitCode)
		}
		tool("exec", string(input), result, item.Status == "failed")
	case "McpToolCall":
		var result struct {
			Content []claudeCodeBlock `json:"content"`
			IsError bool              `json:"isError"`
		}
		text := string(item.Result)
		if json.Unmarshal(item.Result, &result) == nil && result.Content != nil {
			content, _ := json.Marshal(result.Content)
			text = claudeCodeResultText(content)
		}
		tool(item.Server+"."+item.Tool, string(item.Arguments), text, item.Status == "failed" || result.IsError)
	case "FileChange":
		var input strings.Builder
		for _, path := range slices.Sorted(maps.Keys(item.Changes)) {
			change := item.Changes[path]
			fmt.Fprintf(&input, "%s %s", change.Type, path)
			if change.MovePath != nil {
				fmt.Fprintf(&input, " -> %s", *change.MovePath)
			}
			input.WriteString("\n")
			if body := change.UnifiedDiff + change.Content; body != "" {
				input.WriteString(strings.TrimSuffix(body, "\n"))
				input.WriteString("\n")
			}
		}
		tool("apply_patch", strings.TrimSuffix(input.String(), "\n"), strings.TrimSuffix(item.Stdout+item.Stderr, "\n"), item.Status != "completed")
	case "Extension":
		r.renderExtension(w, line, item, assistant)
	case "Plan":
		assistant()
		fmt.Fprintf(w, "### Plan\n\n%s\n\n", ansi.Strip(item.Text))
		r.thinking = false
	case "ImageView":
		assistant()
		fmt.Fprint(w, "[image]\n\n")
		r.thinking = false
	case "ContextCompaction":
		heading("Compacted")
		r.Turn = ""
		fmt.Fprint(w, "(Codex keeps its summary encrypted)\n\n")
	case "SubAgentActivity", "CollabAgentToolCall":
		// A subagent's, which is kept raw only.
	default:
		return false
	}
	return true
}

// renderExtension writes an Extension item as a tool named by its kind, whose input
// is what else the item says but its results.
func (r *codexRendered) renderExtension(w io.Writer, line codexLine, item codexItem, assistant func()) {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(line.Payload.Item, &fields)
	for _, key := range []string{"type", "kind", "id", "results"} {
		delete(fields, key)
	}
	input, _ := json.Marshal(fields)
	assistant()
	fmt.Fprintf(w, "### Tool: %s\n\n%s\n\n", ansi.Strip(item.Kind), fenced(string(input)))
	if item.Results != nil {
		lines := make([]string, 0, len(item.Results))
		for _, res := range item.Results {
			lines = append(lines, strings.TrimSpace(res.Title+" "+res.URL))
		}
		fmt.Fprintf(w, "### Result\n\n%s\n\n", fenced(strings.Join(lines, "\n")))
	}
	r.thinking = false
}
