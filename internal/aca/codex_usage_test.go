package aca_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
)

// codexUsageFixture is the session file testdata/codex/<version>/usage/<name>.jsonl,
// cut from a session Codex wrote, whose cli_version is version, and redacted: its
// first n lines, or all of them when n is 0.
func codexUsageFixture(t *testing.T, version, name string, n int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "codex", version, "usage", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(strings.TrimSuffix(string(data), "\n"), "\n")
	if n == 0 {
		n = len(lines)
	}
	if n > len(lines) {
		t.Fatalf("%s has %d lines, not %d", name, len(lines), n)
	}
	return strings.Join(lines[:n], "")
}

// codexUsageHome is a home where Codex keeps text as the session codexSession, and
// the file it is in.
func codexUsageHome(t *testing.T, text string) (home, file string) {
	t.Helper()
	home = t.TempDir()
	file = codexFile(home, codexSession)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, file
}

func codexUsage(t *testing.T, home string) ([]aca.Usage, []string, error) {
	t.Helper()
	return aca.Codex{}.Usage(aca.Launch{UserHome: home, SessionID: codexSession, BackupDir: t.TempDir()})
}

func usageRowsText(rows []aca.Usage) string {
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%s/%q %d resets %d at %s", r.Window, r.Bucket, r.UsedPercent, r.ResetsAt.Unix(), r.ObservedAt.UTC().Format(time.RFC3339Nano))
		if r.Limited {
			b.WriteString(" limited")
		}
		b.WriteString("; ")
	}
	return b.String()
}

// TestCodexUsage reads the session files Codex wrote on each plan recorded: a row per
// window of the newest codex snapshot, in the bucket "", keyed by its period, observed
// when its line was written; at a limit, behind premium's lines with no window, the
// newest codex snapshot is limited, whether it says 100 or 99, observed at the error.
// The session file is to be followed.
func TestCodexUsage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		version, name string
		lines         int
		want          string
	}{
		{"0.167.0", "usage-plus", 0, `10080m/"" 2 resets 1794405113 at 2026-11-08T14:18:51.13Z; `},
		{"0.142.3", "usage-free", 0, `43200m/"" 50 resets 1785234449 at 2026-07-04T19:44:34.611Z; `},
		{"0.163.0", "usage-window-zero", 0, `10080m/"" 68 resets 1793085952 at 2026-10-20T19:04:13.868Z; `},
		{"0.163.0", "usage-window-zero", 4, `10080m/"" 67 resets 1793085952 at 2026-10-20T19:03:41.76Z; `},
		{"0.150.1", "usage-limit-go", 4, `43200m/"" 99 resets 1790686146 at 2026-08-30T13:09:52.253Z; `},
		{"0.150.1", "usage-limit-go", 5, `43200m/"" 99 resets 1790686146 at 2026-08-30T13:09:52.253Z; `},
		{"0.150.1", "usage-limit-go", 6, `43200m/"" 99 resets 1790686146 at 2026-08-30T13:09:52.863Z limited; `},
		{"0.150.1", "usage-limit-go", 0, `43200m/"" 99 resets 1790686146 at 2026-08-30T13:27:58.386Z limited; `},
		{"0.151.0", "usage-limit-plus", 0, `10080m/"" 100 resets 1788773288 at 2026-09-01T05:22:26.554Z limited; `},
	} {
		home, file := codexUsageHome(t, codexUsageFixture(t, tc.version, tc.name, tc.lines))
		rows, follow, err := codexUsage(t, home)
		if err != nil || usageRowsText(rows) != tc.want {
			t.Errorf("%s %s, %d lines: usage = %s %v; want %s", tc.version, tc.name, tc.lines, usageRowsText(rows), err, tc.want)
		}
		if !slices.Equal(follow, []string{file}) {
			t.Errorf("%s %s: following %q, want the session file", tc.version, tc.name, follow)
		}
	}
}

// codexRateLimits is a token_count line written at timestamp, whose rate_limits is
// limits.
func codexRateLimits(t *testing.T, timestamp string, limits any) string {
	t.Helper()
	line, err := json.Marshal(map[string]any{"timestamp": timestamp, "type": "event_msg", "payload": map[string]any{
		"type": "token_count", "info": nil, "rate_limits": limits,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return string(line) + "\n"
}

func codexWindow(used float64, minutes, resets int64) map[string]any {
	return map[string]any{"used_percent": used, "window_minutes": minutes, "resets_at": resets}
}

// TestCodexUsageBuckets: a bucket other than codex keeps its name, with the newest of
// its snapshots after the newest codex one; both windows are rows, a percentage is
// rounded, and a window of 0 minutes is skipped. What is older than the codex snapshot
// is not read, nor is a limit error before it.
func TestCodexUsageBuckets(t *testing.T) {
	t.Parallel()
	text := codexUsageFixture(t, "0.150.1", "usage-limit-go", 6) +
		codexRateLimits(t, "2026-08-30T14:00:00.000Z", map[string]any{"limit_id": "gpt-6-spark", "primary": codexWindow(10, 300, 1788120000), "secondary": nil}) +
		codexRateLimits(t, "2026-08-30T14:00:01.000Z", map[string]any{"limit_id": "codex", "primary": codexWindow(12.5, 300, 1788110000), "secondary": codexWindow(40.4, 10080, 1788600000)}) +
		codexRateLimits(t, "2026-08-30T14:00:02.000Z", map[string]any{"limit_id": "gpt-6-spark", "primary": codexWindow(20, 300, 1788120000), "secondary": codexWindow(0, 0, 1788000000)}) +
		codexRateLimits(t, "2026-08-30T14:00:03.000Z", map[string]any{"limit_id": "gpt-6-spark", "primary": nil, "secondary": nil}) +
		codexRateLimits(t, "2026-08-30T14:00:04.000Z", map[string]any{"limit_id": "premium", "primary": nil, "secondary": nil})
	home, _ := codexUsageHome(t, text)
	rows, _, err := codexUsage(t, home)
	want := `300m/"" 13 resets 1788110000 at 2026-08-30T14:00:01Z; 10080m/"" 40 resets 1788600000 at 2026-08-30T14:00:01Z; ` +
		`300m/"gpt-6-spark" 20 resets 1788120000 at 2026-08-30T14:00:02Z; `
	if err != nil || usageRowsText(rows) != want {
		t.Errorf("usage = %s %v; want %s", usageRowsText(rows), err, want)
	}
}

// TestCodexUsageUnknown: nothing is told by a file whose rate_limits are null, or
// whose every field is, by a session Codex has not named yet, nor by a file gone.
func TestCodexUsageUnknown(t *testing.T) {
	t.Parallel()
	home, file := codexUsageHome(t, codexUsageFixture(t, "0.142.3", "usage-null", 0))
	if _, follow, err := codexUsage(t, home); !errors.Is(err, aca.ErrNoUsage) || !slices.Equal(follow, []string{file}) {
		t.Errorf("null rate limits: %v, following %q", err, follow)
	}
	if _, _, err := codexUsage(t, t.TempDir()); !errors.Is(err, aca.ErrNoUsage) {
		t.Errorf("no session file: %v", err)
	}
	if _, _, err := (aca.Codex{}).Usage(aca.Launch{UserHome: t.TempDir(), SessionID: "not-a-session"}); !errors.Is(err, aca.ErrNoUsage) {
		t.Errorf("no session: %v", err)
	}
}

// TestCodexUsageFurtherBackThanABlock: the newest codex snapshot can be far from the
// end, behind a tool's long output and lines longer than what is read at a time, up
// to 1 MiB back; further than that, nothing is told.
func TestCodexUsageFurtherBackThanABlock(t *testing.T) {
	t.Parallel()
	output := func(size int) string {
		line, err := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{
			"type": "function_call_output", "call_id": "call_01a1abcc", "output": strings.Repeat("x", size),
		}})
		if err != nil {
			t.Fatal(err)
		}
		return string(line) + "\n"
	}
	text := codexUsageFixture(t, "0.167.0", "usage-plus", 0)
	for _, size := range []int{100 << 10, 70 << 10, 300} {
		text += output(size)
	}
	home, _ := codexUsageHome(t, text)
	rows, _, err := codexUsage(t, home)
	if want := `10080m/"" 2 resets 1794405113 at 2026-11-08T14:18:51.13Z; `; err != nil || usageRowsText(rows) != want {
		t.Errorf("usage = %s %v; want %s", usageRowsText(rows), err, want)
	}

	home, _ = codexUsageHome(t, text+output(1<<20))
	if rows, _, err := codexUsage(t, home); !errors.Is(err, aca.ErrNoUsage) {
		t.Errorf("further back than 1 MiB: usage = %s %v", usageRowsText(rows), err)
	}
}
