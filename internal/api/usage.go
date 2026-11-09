package api

import (
	"fmt"
	"strings"
	"time"
)

// UsageBucket is one bucket of models of an AI coding agent's account with its
// windows, every one a gate of its own: the bucket is available only while none of
// them is limited (the wdx doc USAGE). The daemon stores nothing of this: the CLI
// and tide derive it here, from the same rows, so that they say the same thing.
type UsageBucket struct {
	Aca     string
	Bucket  string // "" for the one every model draws on
	Windows []UsageWindow
	// Available is whether no window is limited; AvailableAt, when not, the latest
	// reset among the limited ones, zero when none of them gives a reset.
	Available   bool
	AvailableAt time.Time
	// ObservedAt is the newest observation among the windows.
	ObservedAt time.Time
}

// UsageBuckets groups rows, as listUsage returns them, by ACA and bucket, in their
// order: the one every model draws on first for each ACA, as its name sorts first.
// The store lists a bucket's windows together, so a bucket is one group however
// many windows it shares with another, as Antigravity's share a session and a week.
func UsageBuckets(rows []UsageWindow) []UsageBucket {
	var buckets []UsageBucket
	for _, w := range rows {
		i := len(buckets) - 1
		if i < 0 || buckets[i].Aca != w.Aca || buckets[i].Bucket != w.Bucket {
			buckets = append(buckets, UsageBucket{Aca: w.Aca, Bucket: w.Bucket, Available: true})
			i++
		}
		b := &buckets[i]
		b.Windows = append(b.Windows, w)
		if w.ObservedAt.After(b.ObservedAt) {
			b.ObservedAt = w.ObservedAt
		}
		if w.Limited {
			b.Available = false
			if w.ResetsAt != nil && w.ResetsAt.After(b.AvailableAt) {
				b.AvailableAt = *w.ResetsAt
			}
		}
	}
	return buckets
}

// usageLabels is what a person reads for the windows wdx knows, by ACA and window;
// any other window shows its key as given, so a new one appears unnamed, not hidden.
var usageLabels = map[string]map[string]string{
	"claude_code": {"five_hour": "Session (5 h)", "seven_day": "Week", "spend_limit": "Spend"},
	"codex":       {"300m": "Session (5 h)", "10080m": "Week", "43200m": "Month"},
	"antigravity": {"5h": "Session (5 h)", "weekly": "Week"},
	"cursor":      {"month": "Month"},
}

// usageBucketLabels is what a person reads for the buckets wdx knows, by ACA and
// bucket; any other bucket shows its name as given, as the windows do.
var usageBucketLabels = map[string]map[string]string{
	"antigravity": {"gemini": "Gemini models", "3p": "Claude and GPT models", "gemini-argon-wtu": "Gemini 4 Argon"},
	"cursor":      {"auto": "Auto", "api": "API"},
}

// UsageLabel is what a person reads for the window of aca: its label when wdx knows
// the window, its key as given otherwise.
func UsageLabel(aca, window string) string {
	if label, ok := usageLabels[aca][window]; ok {
		return label
	}
	return window
}

// UsageBucketLabel is what a person reads for the bucket of aca: its label when wdx
// knows the bucket, its name as given otherwise, "" for the one every model draws on.
func UsageBucketLabel(aca, bucket string) string {
	if label, ok := usageBucketLabels[aca][bucket]; ok {
		return label
	}
	return bucket
}

// ResetText says when t comes, as of now, in the local time: in 2 h 10 min under a
// day ahead, the date and time otherwise, and "now" once passed.
func ResetText(t, now time.Time) string {
	left := t.Sub(now)
	switch {
	case left <= 0:
		return "now"
	case left < time.Minute:
		return "in under a minute"
	case left < time.Hour:
		return fmt.Sprintf("in %d min", int(left.Minutes()))
	case left < 24*time.Hour:
		h, m := int(left.Hours()), int(left.Minutes())%60
		if m == 0 {
			return fmt.Sprintf("in %d h", h)
		}
		return fmt.Sprintf("in %d h %d min", h, m)
	}
	return t.Local().Format("Jan 2 15:04")
}

// AvailabilityText is the bucket's line: available, or limited and until when.
func AvailabilityText(b UsageBucket, now time.Time) string {
	switch {
	case b.Available:
		return "available"
	case b.AvailableAt.IsZero():
		return "limited"
	}
	return "limited, resets " + ResetText(b.AvailableAt, now)
}

// LimitedText is what a person reads of the limit of an agent of aca: the bucket
// by its label, but for the one every model draws on, and when it resets.
func LimitedText(aca string, l AgentLimited, now time.Time) string {
	var parts []string
	if label := UsageBucketLabel(aca, l.Bucket); label != "" {
		parts = append(parts, label)
	}
	if l.ResetsAt != nil {
		parts = append(parts, "resets "+ResetText(*l.ResetsAt, now))
	}
	if len(parts) == 0 {
		return "no reset told"
	}
	return strings.Join(parts, ", ")
}
