package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/db/dbtest"
)

func newUsageStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	dbtest.Seed(t, path)
	d, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return NewStore(d)
}

func usageText(list []Usage) string {
	var text string
	for _, u := range list {
		text += u.ACA + " " + u.Window + "/" + u.Bucket + " " + itoa(u.UsedPercent)
		if u.Limited {
			text += " limited"
		}
		text += "; "
	}
	return text
}

func itoa(n int) string { return time.Duration(n).String()[:len(time.Duration(n).String())-2] }

// TestStoreUsage: rows are written by ACA, window and bucket, the latest observation
// winning, a window at 100 being limited, and one no longer told dropped once its
// reset has passed, kept until then.
func TestStoreUsage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newUsageStore(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	later := now.Add(time.Minute)
	first := []aca.Usage{
		{Window: "five_hour", UsedPercent: 54, ResetsAt: now.Add(2 * time.Hour), ObservedAt: now},
		{Window: "seven_day", UsedPercent: 100, ResetsAt: now.Add(72 * time.Hour), ObservedAt: now},
	}
	if changed, err := s.setUsage(ctx, "claude_code", "", first, now); err != nil || !changed {
		t.Fatalf("first write: %v, %v", changed, err)
	}
	list, err := s.Usage(ctx)
	if err != nil || usageText(list) != "claude_code five_hour/ 54; claude_code seven_day/ 100 limited; " {
		t.Fatalf("after the first write: %s, %v", usageText(list), err)
	}
	if !list[0].ResetsAt.Equal(now.Add(2*time.Hour)) || !list[0].ObservedAt.Equal(now) || list[0].AgentID != "" {
		t.Errorf("row = %+v", list[0])
	}

	// An older observation of the same window, as a stale file read late, changes nothing.
	stale := []aca.Usage{{Window: "five_hour", UsedPercent: 10, ResetsAt: now.Add(2 * time.Hour), ObservedAt: now.Add(-time.Hour)}}
	if changed, err := s.setUsage(ctx, "claude_code", "", stale, later); err != nil || changed {
		t.Errorf("a stale write: %v, %v", changed, err)
	}
	// A newer one of the five-hour window alone: the week is kept, its reset being ahead.
	newer := []aca.Usage{{Window: "five_hour", UsedPercent: 60, ResetsAt: now.Add(2 * time.Hour), ObservedAt: later}}
	if changed, err := s.setUsage(ctx, "claude_code", "", newer, later); err != nil || !changed {
		t.Errorf("a newer write: %v, %v", changed, err)
	}
	if list, err = s.Usage(ctx); err != nil || usageText(list) != "claude_code five_hour/ 60; claude_code seven_day/ 100 limited; " {
		t.Errorf("after a newer write: %s, %v", usageText(list), err)
	}
	// Once the week's reset has passed and it is not told, it is dropped; a row whose
	// own reset has passed is not written either.
	after := now.Add(73 * time.Hour)
	passed := []aca.Usage{
		{Window: "five_hour", UsedPercent: 5, ResetsAt: after.Add(time.Hour), ObservedAt: after},
		{Window: "old", UsedPercent: 99, ResetsAt: after.Add(-time.Second), ObservedAt: after},
	}
	if changed, err := s.setUsage(ctx, "claude_code", "", passed, after); err != nil || !changed {
		t.Errorf("a write past the reset: %v, %v", changed, err)
	}
	if list, err = s.Usage(ctx); err != nil || usageText(list) != "claude_code five_hour/ 5; " {
		t.Errorf("after the week's reset: %s, %v", usageText(list), err)
	}
}

// TestStoreUsageByACA: one ACA's rows leave another's alone, a bucket is a row of its
// own, and a window without a reset is dropped as soon as it is not told.
func TestStoreUsageByACA(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newUsageStore(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	if _, err := s.setUsage(ctx, "codex", "", []aca.Usage{{Window: "weekly", UsedPercent: 20, ResetsAt: now.Add(time.Hour), ObservedAt: now}}, now); err != nil {
		t.Fatal(err)
	}
	rows := []aca.Usage{
		{Window: "seven_day", UsedPercent: 37, ResetsAt: now.Add(time.Hour), ObservedAt: now},
		{Window: "seven_day", Bucket: "Fable", UsedPercent: 35, ResetsAt: now.Add(time.Hour), ObservedAt: now},
		{Window: "spend_limit", UsedPercent: 63, ObservedAt: now},
	}
	if _, err := s.setUsage(ctx, "claude_code", "", rows, now); err != nil {
		t.Fatal(err)
	}
	list, err := s.Usage(ctx)
	if err != nil || usageText(list) != "claude_code seven_day/ 37; claude_code seven_day/Fable 35; claude_code spend_limit/ 63; codex weekly/ 20; " {
		t.Fatalf("both ACAs: %s, %v", usageText(list), err)
	}
	if !list[2].ResetsAt.IsZero() {
		t.Errorf("spend_limit resets at %v", list[2].ResetsAt)
	}
	if _, err := s.setUsage(ctx, "claude_code", "", rows[:2], now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if list, err = s.Usage(ctx); err != nil || usageText(list) != "claude_code seven_day/ 37; claude_code seven_day/Fable 35; codex weekly/ 20; " {
		t.Errorf("spend_limit no longer told: %s, %v", usageText(list), err)
	}
}
