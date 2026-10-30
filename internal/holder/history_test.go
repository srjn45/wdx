package holder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/screen"
)

func numberedLines(from, to int64) []string {
	var lines []string
	for i := from; i < to; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	return lines
}

func openTestHistory(t *testing.T, dir string, limit int64) *historyWriter {
	t.Helper()
	h, err := openHistory(dir, limit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.close() })
	return h
}

func TestHistoryRead(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "history")
	h := openTestHistory(t, dir, 100_000)
	if err := h.append(numberedLines(0, 25_000)...); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		read        func() (History, error)
		from, count int64
	}{
		{"start", func() (History, error) { return ReadHistory(dir, 0, 3) }, 0, 3},
		{"across segments", func() (History, error) { return ReadHistory(dir, 9_998, 5) }, 9_998, 5},
		{"past the end", func() (History, error) { return ReadHistory(dir, 24_998, 10) }, 24_998, 2},
		{"after the end", func() (History, error) { return ReadHistory(dir, 30_000, 10) }, 25_000, 0},
		{"before the start", func() (History, error) { return ReadHistory(dir, -5, 2) }, 0, 2},
		{"tail", func() (History, error) { return TailHistory(dir, 4) }, 24_996, 4},
		{"tail of more than kept", func() (History, error) { return TailHistory(dir, 30_000) }, 0, 25_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.read()
			if err != nil {
				t.Fatal(err)
			}
			want := History{From: tc.from, Next: 25_000, First: 0, Lines: numberedLines(tc.from, tc.from+tc.count)}
			if got.From != want.From || got.Next != want.Next || got.First != want.First || !slices.Equal(got.Lines, want.Lines) {
				t.Fatalf("got from %d next %d first %d, %d lines, want from %d, %d lines", got.From, got.Next, got.First, len(got.Lines), want.From, len(want.Lines))
			}
		})
	}
	for _, name := range []string{"0000000000.log", "0000010000.log", "0000020000.log"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", name, info, err)
		}
	}
}

func TestHistoryTrimmed(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "history")
	h := openTestHistory(t, dir, 25_000)
	if err := h.append(numberedLines(0, 35_001)...); err != nil {
		t.Fatal(err)
	}
	// Whole segments go: between 15,000 and 25,000 lines stay.
	got, err := ReadHistory(dir, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.First != 20_000 || got.From != 20_000 || got.Next != 35_001 || !slices.Equal(got.Lines, numberedLines(20_000, 20_002)) {
		t.Fatalf("got first %d from %d next %d %q", got.First, got.From, got.Next, got.Lines)
	}
}

func TestHistoryContinued(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "history")
	h, err := openHistory(dir, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.append("one", "two\nlines"); err != nil {
		t.Fatal(err)
	}
	h.close()
	// A holder killed while writing leaves a line without its newline.
	f, err := os.OpenFile(segmentPath(dir, 0), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("unfinish"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if got, err := TailHistory(dir, 10); err != nil || got.Next != 2 || !slices.Equal(got.Lines, []string{"one", "two lines"}) {
		t.Fatalf("got %+v, %v, want the unfinished line left out", got, err)
	}

	h = openTestHistory(t, dir, 100_000)
	if err := h.append("three"); err != nil {
		t.Fatal(err)
	}
	if got, err := TailHistory(dir, 10); err != nil || got.Next != 3 || !slices.Equal(got.Lines, []string{"one", "two lines", "three"}) {
		t.Fatalf("got %+v, %v, want the numbering continued", got, err)
	}
}

func TestHistoryNone(t *testing.T) {
	t.Parallel()
	got, err := TailHistory(filepath.Join(t.TempDir(), "missing"), 10)
	if err != nil || got.Next != 0 || len(got.Lines) != 0 {
		t.Fatalf("got %+v, %v, want an empty history", got, err)
	}
}

// TestHistoryMarksReset: when the vt fails on the output, history marks
// the gap and goes on with the lines that scroll off after it.
func TestHistoryMarksReset(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "history")
	h := &holder{
		log:     slog.New(slog.DiscardHandler),
		screen:  screen.New(80, 2, nil),
		size:    Size{Cols: 80, Rows: 2},
		history: openTestHistory(t, dir, 1000),
		spool:   newTestSpool(t, spoolSegment, spoolBacklog),
	}
	defer h.screen.Close()
	h.screen.FailOn('z')
	for _, out := range []string{"1\r\n2\r\n3\r\n", "\x1b[z\x1b[2J\x1b[H", "a\r\nb\r\nc\r\n"} {
		if err := h.spool.append([]byte(out)); err != nil {
			t.Fatal(err)
		}
		h.feed(h.vtPos, []byte(out))
	}
	got, err := ReadHistory(dir, 0, 10)
	if want := []string{"1", "2", MarkInterrupted, "a", "b"}; err != nil || !slices.Equal(got.Lines, want) {
		t.Fatalf("got %q, %v, want %q", got.Lines, err, want)
	}
}

// TestLagLogged feeds the vt less than the output holds, then the rest:
// the holder logs when it falls behind the limit, and once it catches up,
// how far and for how long, and at what rate it parsed (docs/VT.md).
func TestLagLogged(t *testing.T) {
	t.Parallel()
	var logged bytes.Buffer
	h := &holder{
		log:    slog.New(slog.NewJSONHandler(&logged, nil)),
		screen: screen.New(80, 24, nil),
		size:   Size{Cols: 80, Rows: 24},
		spool:  newTestSpool(t, spoolSegment, spoolBacklog),
		lag:    lag{limit: 100},
	}
	defer h.screen.Close()
	out := []byte(strings.Repeat("x", 300))
	if err := h.spool.append(out); err != nil {
		t.Fatal(err)
	}
	h.feed(0, out[:50])    // 250 behind: past the limit
	h.feed(50, out[50:60]) // still behind
	h.feed(60, out[60:])   // caught up
	h.feed(300, nil)       // keeping up: nothing logged
	var lines []map[string]any
	dec := json.NewDecoder(&logged)
	for dec.More() {
		var line map[string]any
		if err := dec.Decode(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if len(lines) != 2 || lines[0]["msg"] != "vt fell behind" || lines[0]["lag_bytes"] != 250.0 {
		t.Fatalf("want the vt behind by 250 bytes, then caught up, got %v", lines)
	}
	up := lines[1]
	if up["msg"] != "vt caught up" || up["max_lag_bytes"] != 250.0 || up["bytes"] != 250.0 || up["duration_ms"] == nil || up["bytes_per_second"].(float64) <= 0 {
		t.Fatalf("want caught up after parsing 250 bytes, 250 the most behind, got %v", up)
	}
}
