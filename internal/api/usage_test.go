package api

import (
	"testing"
	"time"
)

func at(h int) *time.Time {
	t := time.Date(2026, 11, 8, h, 0, 0, 0, time.UTC)
	return &t
}

// TestUsageBuckets: rows group by ACA and bucket; every window gates the bucket, so
// one limited window makes it unavailable until its reset, the latest reset among the
// limited ones when several are. Limited alone gates, whatever the percentage, as
// Codex may stop at 99 and say it is limited.
func TestUsageBuckets(t *testing.T) {
	t.Parallel()
	rows := []UsageWindow{
		{Aca: "claude_code", Window: "five_hour", UsedPercent: 100, Limited: true, ResetsAt: at(21), ObservedAt: *at(16)},
		{Aca: "claude_code", Window: "seven_day", UsedPercent: 42, ResetsAt: at(23), ObservedAt: *at(17)},
		{Aca: "claude_code", Bucket: "Fable", Window: "seven_day", UsedPercent: 35, ResetsAt: at(23), ObservedAt: *at(15)},
		{Aca: "codex", Window: "weekly", UsedPercent: 99, Limited: true, ObservedAt: *at(12)},
	}
	got := UsageBuckets(rows)
	if len(got) != 3 {
		t.Fatalf("buckets = %+v", got)
	}
	if b := got[0]; b.Aca != "claude_code" || b.Bucket != "" || len(b.Windows) != 2 || b.Available || !b.AvailableAt.Equal(*at(21)) || !b.ObservedAt.Equal(*at(17)) {
		t.Errorf("the session limited: %+v", b)
	}
	if b := got[1]; b.Bucket != "Fable" || !b.Available || !b.AvailableAt.IsZero() || len(b.Windows) != 1 {
		t.Errorf("the Fable bucket: %+v", b)
	}
	if b := got[2]; b.Aca != "codex" || b.Available || !b.AvailableAt.IsZero() {
		t.Errorf("a limit with no reset: %+v", b)
	}
	if got := AvailabilityText(got[0], *at(19)); got != "limited, resets in 2 h" {
		t.Errorf("availability = %q", got)
	}
	if got := AvailabilityText(got[1], *at(19)); got != "available" {
		t.Errorf("availability = %q", got)
	}
	if got := AvailabilityText(got[2], *at(19)); got != "limited" {
		t.Errorf("availability = %q", got)
	}
	// Both windows limited: the later reset frees the bucket.
	rows[1].Limited = true
	if b := UsageBuckets(rows)[0]; b.Available || !b.AvailableAt.Equal(*at(23)) {
		t.Errorf("both limited: %+v", b)
	}
	if len(UsageBuckets(nil)) != 0 {
		t.Error("no rows make a bucket")
	}
}

func TestUsageLabel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ aca, window, want string }{
		{"claude_code", "five_hour", "Session (5 h)"},
		{"claude_code", "seven_day", "Week"},
		{"claude_code", "two_hour", "two_hour"},
		{"codex", "weekly", "weekly"},
	} {
		if got := UsageLabel(tc.aca, tc.window); got != tc.want {
			t.Errorf("UsageLabel(%s, %s) = %q, want %q", tc.aca, tc.window, got, tc.want)
		}
	}
}

func TestResetText(t *testing.T) {
	t.Parallel()
	now := *at(12)
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{-time.Second, "now"}, {30 * time.Second, "in under a minute"}, {5 * time.Minute, "in 5 min"},
		{2 * time.Hour, "in 2 h"}, {2*time.Hour + 10*time.Minute, "in 2 h 10 min"}, {23*time.Hour + 59*time.Minute, "in 23 h 59 min"},
	} {
		if got := ResetText(now.Add(tc.in), now); got != tc.want {
			t.Errorf("ResetText(+%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := ResetText(now.Add(72*time.Hour), now); got != now.Add(72*time.Hour).Local().Format("Jan 2 15:04") {
		t.Errorf("days ahead = %q", got)
	}
}
