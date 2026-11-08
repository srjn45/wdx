package aca_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
)

func antigravityQuotaInput(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "antigravity", "1.5.3", "status", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// antigravityUsage writes input through the agent's status line, as Antigravity runs
// it, and reads Usage from the file it kept, with the file's time.
func antigravityUsage(t *testing.T, input []byte) ([]aca.Usage, time.Time, error) {
	t.Helper()
	l := antigravityLaunch(t)
	antigravityStatusLine(t, l, input)
	info, err := os.Stat(filepath.Join(l.BackupDir, "antigravity-status.json"))
	if err != nil {
		t.Fatal(err)
	}
	rows, follow, err := (aca.Antigravity{}).Usage(l)
	if follow != nil {
		t.Errorf("following %q, want nothing", follow)
	}
	return rows, info.ModTime(), err
}

func checkUsage(t *testing.T, got, want []aca.Usage) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Window != want[i].Window || got[i].Bucket != want[i].Bucket || got[i].UsedPercent != want[i].UsedPercent || !got[i].ResetsAt.Equal(want[i].ResetsAt) || !got[i].ObservedAt.Equal(want[i].ObservedAt) {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func rfc3339(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

// TestAntigravityUsage reads quota as 1.5.3 gives it on a Google AI Pro plan: a row
// per id in the order of the ids, split into bucket and window at the last "-", the
// percentage used rounded down, observed when the status line wrote the file. Every
// window is shown, an untouched one with the reset Antigravity gives it.
func TestAntigravityUsage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		input string
		want  []aca.Usage
	}{
		{"after start", "with-quota.json", []aca.Usage{
			{Bucket: "3p", Window: "5h", UsedPercent: 0, ResetsAt: rfc3339(t, "2026-11-08T19:15:12Z")},
			{Bucket: "3p", Window: "weekly", UsedPercent: 0, ResetsAt: rfc3339(t, "2026-11-14T03:12:32Z")},
			{Bucket: "gemini", Window: "5h", UsedPercent: 0, ResetsAt: rfc3339(t, "2026-11-08T19:15:12Z")},
			{Bucket: "gemini-argon-wtu", Window: "weekly", UsedPercent: 0, ResetsAt: rfc3339(t, "2026-11-15T14:15:12Z")},
			{Bucket: "gemini", Window: "weekly", UsedPercent: 0, ResetsAt: rfc3339(t, "2026-11-14T03:13:02Z")},
		}},
		{"after a refresh", "after-refresh.json", []aca.Usage{
			{Bucket: "3p", Window: "5h", UsedPercent: 0, ResetsAt: rfc3339(t, "2026-11-08T19:21:57Z")},
			{Bucket: "3p", Window: "weekly", UsedPercent: 0, ResetsAt: rfc3339(t, "2026-11-14T03:12:32Z")},
			{Bucket: "gemini", Window: "5h", UsedPercent: 0, ResetsAt: rfc3339(t, "2026-11-08T19:15:12Z")},
			{Bucket: "gemini-argon-wtu", Window: "weekly", UsedPercent: 0, ResetsAt: rfc3339(t, "2026-11-15T14:21:57Z")},
			{Bucket: "gemini", Window: "weekly", UsedPercent: 0, ResetsAt: rfc3339(t, "2026-11-14T03:13:02Z")},
		}},
		// Made by hand, no bucket having been used up yet: gemini-5h has no fraction,
		// as Antigravity leaves out a 0, and gemini-weekly has 0.4 % left.
		{"used up, made up", "exhausted-made-up.json", []aca.Usage{
			{Bucket: "3p", Window: "5h", UsedPercent: 0, ResetsAt: rfc3339(t, "2026-11-08T19:21:57Z")},
			{Bucket: "3p", Window: "weekly", UsedPercent: 0, ResetsAt: rfc3339(t, "2026-11-14T03:12:32Z")},
			{Bucket: "gemini", Window: "5h", UsedPercent: 100, ResetsAt: rfc3339(t, "2026-11-08T19:15:12Z")},
			{Bucket: "gemini-argon-wtu", Window: "weekly", UsedPercent: 0, ResetsAt: rfc3339(t, "2026-11-15T14:21:57Z")},
			{Bucket: "gemini", Window: "weekly", UsedPercent: 99, ResetsAt: rfc3339(t, "2026-11-14T03:13:02Z")},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, observed, err := antigravityUsage(t, antigravityQuotaInput(t, tc.input))
			if err != nil {
				t.Fatal(err)
			}
			for i := range tc.want {
				tc.want[i].ObservedAt = observed
			}
			checkUsage(t, got, tc.want)
		})
	}
}

// TestAntigravityUsageSplit: the bucket and window Usage takes from each id are the
// group and window agy -p /usage states for it, for every id it lists.
func TestAntigravityUsageSplit(t *testing.T) {
	t.Parallel()
	var printed struct {
		Command struct {
			Data struct {
				Groups []struct {
					Buckets []struct {
						ID                string  `json:"id"`
						Window            string  `json:"window"`
						RemainingFraction float64 `json:"remaining_fraction"`
						ResetTime         string  `json:"reset_time"`
					} `json:"buckets"`
				} `json:"groups"`
			} `json:"data"`
		} `json:"command"`
	}
	if err := json.Unmarshal(antigravityQuotaInput(t, "usage-print.json"), &printed); err != nil {
		t.Fatal(err)
	}
	quota := map[string]any{}
	group := map[string]int{}
	for g, each := range printed.Command.Data.Groups {
		for _, b := range each.Buckets {
			quota[b.ID] = map[string]any{"remaining_fraction": b.RemainingFraction, "reset_time": b.ResetTime}
			group[b.ID] = g
		}
	}
	input, err := json.Marshal(map[string]any{"quota": quota})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := antigravityUsage(t, input)
	if err != nil || len(got) != 5 || len(got) != len(quota) {
		t.Fatalf("usage = %+v, %v, want a row for each of %d ids", got, err, len(quota))
	}
	bucketOf := map[int]string{}
	for _, g := range printed.Command.Data.Groups {
		for _, b := range g.Buckets {
			var row *aca.Usage
			for i := range got {
				if got[i].Bucket+"-"+got[i].Window == b.ID {
					row = &got[i]
				}
			}
			if row == nil || row.Window != b.Window {
				t.Errorf("%s: row %+v, want window %s", b.ID, row, b.Window)
				continue
			}
			if name, ok := bucketOf[group[b.ID]]; ok && name != row.Bucket {
				t.Errorf("%s: bucket %q, the rest of its group %q", b.ID, row.Bucket, name)
			}
			bucketOf[group[b.ID]] = row.Bucket
		}
	}
	if len(bucketOf) != 3 {
		t.Errorf("buckets %v, want one per group", bucketOf)
	}
}

// TestAntigravityUsageIDs: an id without "-" is both bucket and window; a fraction is
// rounded down whatever the float makes of it, so 100 is only nothing left.
func TestAntigravityUsageIDs(t *testing.T) {
	t.Parallel()
	got, _, err := antigravityUsage(t, []byte(`{"quota": {
		"daily": {"remaining_fraction": 0.34, "reset_time": "2026-11-09T00:00:00Z"},
		"x-none": {"remaining_fraction": 0},
		"y-full": {"remaining_fraction": 1, "reset_time": "2026-11-09T00:00:00Z"},
		"z-nothing": {}}}`))
	if err != nil {
		t.Fatal(err)
	}
	reset := rfc3339(t, "2026-11-09T00:00:00Z")
	want := []aca.Usage{
		{Bucket: "daily", Window: "daily", UsedPercent: 66, ResetsAt: reset},
		{Bucket: "x", Window: "none", UsedPercent: 100},
		{Bucket: "y", Window: "full", UsedPercent: 0, ResetsAt: reset},
	}
	for i := range want {
		if i < len(got) {
			want[i].ObservedAt = got[i].ObservedAt
		}
	}
	checkUsage(t, got, want)
}

// TestAntigravityUsageUnknown: nothing is told before the status line first ran, by
// a file without quota (the first second of a session, or 1.5.2), or with nothing in
// it; a file or a reset that does not parse is an error to log.
func TestAntigravityUsageUnknown(t *testing.T) {
	t.Parallel()
	l := antigravityLaunch(t)
	if _, _, err := (aca.Antigravity{}).Usage(l); !errors.Is(err, aca.ErrNoUsage) {
		t.Errorf("before the status line ran: %v", err)
	}
	if _, _, err := (aca.Antigravity{}).Usage(aca.Launch{}); !errors.Is(err, aca.ErrNoUsage) {
		t.Errorf("no backup: %v", err)
	}
	for name, input := range map[string][]byte{
		"before quota":     antigravityQuotaInput(t, "before-quota.json"),
		"1.5.2":            antigravityStatusInput(t, "mid-session.json"),
		"empty quota":      []byte(`{"quota": {}}`),
		"nothing in quota": []byte(`{"quota": {"gemini-5h": {}}}`),
	} {
		if _, _, err := antigravityUsage(t, input); !errors.Is(err, aca.ErrNoUsage) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, input := range map[string][]byte{
		"not JSON":         []byte(`{"quota": {`),
		"a reset in words": []byte(`{"quota": {"gemini-5h": {"remaining_fraction": 1, "reset_time": "tomorrow"}}}`),
	} {
		if _, _, err := antigravityUsage(t, input); err == nil || errors.Is(err, aca.ErrNoUsage) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
