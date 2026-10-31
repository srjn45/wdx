package logging

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// clock is a settable time source.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func day(s string) time.Time {
	t, err := time.ParseInLocation(time.DateTime, s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

func files(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDailyFileRollsOverAtMidnight(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "logs")
	c := &clock{t: day("2026-10-27 23:59:59")}
	f := NewDailyFile(dir, 7, c.now)
	defer f.Close()

	fmt.Fprintln(f, "one")
	c.set(day("2026-10-28 00:00:01"))
	fmt.Fprintln(f, "two")

	if got := files(t, dir); !slices.Equal(got, []string{"wdx-2026-10-27.log", "wdx-2026-10-28.log"}) {
		t.Fatalf("unexpected files %v", got)
	}
	if got := read(t, filepath.Join(dir, "wdx-2026-10-27.log")); got != "one\n" {
		t.Fatalf("unexpected first file %q", got)
	}
	if got := read(t, filepath.Join(dir, "wdx-2026-10-28.log")); got != "two\n" {
		t.Fatalf("unexpected second file %q", got)
	}
}

func TestDailyFileAppends(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := &clock{t: day("2026-10-27 10:00:00")}
	for _, line := range []string{"one", "two"} {
		f := NewDailyFile(dir, 7, c.now)
		fmt.Fprintln(f, line)
		f.Close()
	}
	if got := read(t, filepath.Join(dir, "wdx-2026-10-27.log")); got != "one\ntwo\n" {
		t.Fatalf("unexpected file %q", got)
	}
}

func TestDailyFileDeletesOldFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{
		"wdx-2026-10-19.log", // older than 7 days
		"wdx-2026-10-20.log", // 7 days old: kept
		"wdx-2026-10-26.log",
		"wdx-notadate.log",
		"other-2026-01-01.log",
		"wdx-2026-01-01.txt",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "wdx-2026-01-02.log"), 0o700); err != nil {
		t.Fatal(err)
	}
	f := NewDailyFile(dir, 7, (&clock{t: day("2026-10-27 10:00:00")}).now)
	fmt.Fprintln(f, "x")
	f.Close()

	want := []string{
		"other-2026-01-01.log",
		"wdx-2026-01-01.txt",
		"wdx-2026-01-02.log",
		"wdx-2026-10-20.log",
		"wdx-2026-10-26.log",
		"wdx-2026-10-27.log",
		"wdx-notadate.log",
	}
	if got := files(t, dir); !slices.Equal(got, want) {
		t.Fatalf("got files %v, want %v", got, want)
	}
}

func TestDailyFileDiscardsWhenUnwritable(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f := NewDailyFile(filepath.Join(dir, "logs"), 7, time.Now)
	if n, err := f.Write([]byte("x\n")); n != 2 || err != nil {
		t.Fatalf("want the write discarded, got %d, %v", n, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentWriters writes from many goroutines through two loggers on the
// same file, as two processes would, and checks every line is whole.
func TestConcurrentWriters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const writers, lines = 4, 50
	var wg sync.WaitGroup
	for range 2 {
		f := NewDailyFile(dir, 7, time.Now)
		defer f.Close()
		log := New(f, slog.LevelInfo)
		for w := range writers {
			wg.Go(func() {
				for i := range lines {
					log.Info("line", "writer", w, "i", i, "pad", strings.Repeat("x", 500))
				}
			})
		}
	}
	wg.Wait()

	n := 0
	for _, name := range files(t, dir) {
		s := bufio.NewScanner(strings.NewReader(read(t, filepath.Join(dir, name))))
		s.Buffer(nil, 1<<20)
		for s.Scan() {
			var v map[string]any
			if err := json.Unmarshal(s.Bytes(), &v); err != nil {
				t.Fatalf("broken line %q: %v", s.Text(), err)
			}
			n++
		}
	}
	if n != 2*writers*lines {
		t.Fatalf("want %d lines, got %d", 2*writers*lines, n)
	}
}

func TestNewRedactsCredentialsAndUsesUTC(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	New(&buf, slog.LevelInfo).Info("cloning", "url", "https://user:ghp_secret@github.com/x/y.git", "args", []string{"clone", "https://tok@host/z"})
	line := buf.String()
	if strings.Contains(line, "secret") || strings.Contains(line, "tok@") {
		t.Fatalf("credentials leaked: %s", line)
	}
	if !strings.Contains(line, "https://redacted@github.com/x/y.git") || !strings.Contains(line, "https://redacted@host/z") {
		t.Fatalf("URLs mangled: %s", line)
	}
	var v struct{ Time string }
	if err := json.Unmarshal(buf.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(v.Time, "Z") {
		t.Fatalf("want a UTC time, got %s", v.Time)
	}
}

func TestFromContext(t *testing.T) {
	t.Parallel()
	FromContext(context.Background()).Info("discarded")

	var buf bytes.Buffer
	ctx := NewContext(context.Background(), New(&buf, slog.LevelInfo).With("debug_id", NewDebugID()))
	FromContext(ctx).Info("kept")
	if !strings.Contains(buf.String(), `"msg":"kept","debug_id":"dbg-`) {
		t.Fatalf("unexpected log %s", buf.String())
	}
}

func TestQuiet(t *testing.T) {
	t.Parallel()
	for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo} {
		var b bytes.Buffer
		log := Quiet(New(&b, level)).With("debug_id", "dbg-0123456789ab")
		log.Debug("detail")
		log.Info("step")
		log.Warn("odd")
		log.Error("failed")
		var got []string
		for line := range strings.Lines(b.String()) {
			var v struct{ Level, Msg string }
			if err := json.Unmarshal([]byte(line), &v); err != nil {
				t.Fatal(err)
			}
			got = append(got, v.Level+" "+v.Msg)
		}
		want := []string{"ERROR failed"}
		if level == slog.LevelDebug {
			want = []string{"DEBUG detail", "DEBUG step", "DEBUG odd", "ERROR failed"}
		}
		if !slices.Equal(got, want) {
			t.Errorf("at %s: got %v, want %v", level, got, want)
		}
	}
}

func TestSetRetentionDays(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	today := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	now := today
	f := NewDailyFile(dir, 7, func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	})
	defer f.Close()
	old := filepath.Join(dir, filePrefix+today.AddDate(0, 0, -3).Format(time.DateOnly)+fileSuffix)
	if err := os.WriteFile(old, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("want it kept within 7 days: %v", err)
	}

	f.SetRetentionDays(2)
	mu.Lock()
	now = today.AddDate(0, 0, 1)
	mu.Unlock()
	if _, err := f.Write([]byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("want it deleted with 2 days kept, got %v", err)
	}
}
