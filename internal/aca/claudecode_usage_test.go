package aca_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
)

// TestClaudeCodeUsage reads rate_limits as 2.1.319 gives it on a Max plan: a row per
// window, in the order of their keys, none naming a bucket, observed when the status
// line wrote the file.
func TestClaudeCodeUsage(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	statusLine(t, b.launch, statusInput(t, "with-rate-limits.json"))
	info, err := os.Stat(filepath.Join(b.launch.BackupDir, "claude-status.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := []aca.Usage{
		{Window: "five_hour", UsedPercent: 54, ResetsAt: time.Unix(1794110400, 0), ObservedAt: info.ModTime()},
		{Window: "seven_day", UsedPercent: 51, ResetsAt: time.Unix(1794474000, 0), ObservedAt: info.ModTime()},
	}
	got, follow, err := aca.ClaudeCode{}.Usage(b.launch)
	if err != nil || follow != nil || len(got) != len(want) {
		t.Fatalf("usage = %+v, %v, following %q; want %+v and nothing to follow", got, err, follow, want)
	}
	for i := range want {
		if got[i].Window != want[i].Window || got[i].Bucket != "" || got[i].UsedPercent != want[i].UsedPercent || !got[i].ResetsAt.Equal(want[i].ResetsAt) || !got[i].ObservedAt.Equal(want[i].ObservedAt) {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestClaudeCodeUsageWindows: a window wdx does not know is a row like any other,
// with its percentage rounded and no reset when it gives none, as a gateway's
// spend_limit, whose other fields are left alone.
func TestClaudeCodeUsageWindows(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	statusLine(t, b.launch, []byte(`{"session_id": "s-1", "rate_limits": {"spend_limit": {"used_percentage": 62.8, "used_usd": 314.12, "limit_usd": 500, "period": "monthly"}, "two_hour": {"used_percentage": 100, "resets_at": 1794110400}}}`))
	got, _, err := aca.ClaudeCode{}.Usage(b.launch)
	if err != nil || len(got) != 2 {
		t.Fatalf("usage = %+v, %v", got, err)
	}
	if got[0].Window != "spend_limit" || got[0].UsedPercent != 63 || !got[0].ResetsAt.IsZero() {
		t.Errorf("spend_limit = %+v", got[0])
	}
	if got[1].Window != "two_hour" || got[1].UsedPercent != 100 || !got[1].ResetsAt.Equal(time.Unix(1794110400, 0)) {
		t.Errorf("two_hour = %+v", got[1])
	}
}

// TestClaudeCodeUsageUnknown: nothing is told before the status line first ran, by a
// file without rate_limits (an API-key account, or before the first answer), or by an
// empty block; a file that does not parse is an error to log.
func TestClaudeCodeUsageUnknown(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	if _, _, err := (aca.ClaudeCode{}).Usage(b.launch); !errors.Is(err, aca.ErrNoUsage) {
		t.Errorf("before the status line ran: %v", err)
	}
	statusLine(t, b.launch, statusInput(t, "mid-session.json"))
	if _, _, err := (aca.ClaudeCode{}).Usage(b.launch); !errors.Is(err, aca.ErrNoUsage) {
		t.Errorf("no rate_limits: %v", err)
	}
	statusLine(t, b.launch, []byte(`{"session_id": "s-1", "rate_limits": {}}`))
	if _, _, err := (aca.ClaudeCode{}).Usage(b.launch); !errors.Is(err, aca.ErrNoUsage) {
		t.Errorf("empty rate_limits: %v", err)
	}
	statusLine(t, b.launch, []byte(`{"session_id": "s-1", "rate_limits": {`))
	if _, _, err := (aca.ClaudeCode{}).Usage(b.launch); err == nil || errors.Is(err, aca.ErrNoUsage) {
		t.Errorf("a file that does not parse: %v", err)
	}
	if _, _, err := (aca.ClaudeCode{}).Usage(aca.Launch{}); !errors.Is(err, aca.ErrNoUsage) {
		t.Errorf("no backup: %v", err)
	}
}

// TestUsageNotSupported: the other ACAs tell nothing of their accounts' limits yet.
func TestUsageNotSupported(t *testing.T) {
	t.Parallel()
	for _, a := range []aca.AiCodingAgent{aca.Codex{}, aca.Cursor{}} {
		if _, _, err := a.Usage(aca.Launch{BackupDir: t.TempDir()}); !errors.Is(err, aca.ErrNotSupported) {
			t.Errorf("%s: %v", a.ID(), err)
		}
	}
}
