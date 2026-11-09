package aca

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"slices"
	"time"
)

// codexUsageReadLimit is how much of a session file Usage reads, from its end, for
// the newest snapshot of the codex bucket: at a limit, each refused prompt adds lines
// after it, and nothing older than this is told as the account's usage.
const codexUsageReadLimit = 1 << 20

// codexCommonBucket is the limit_id every model draws on, the bucket "".
const codexCommonBucket = "codex"

var codexTaskComplete = []byte(`"type":"task_complete"`)

// codexUsageLine is what Usage reads of a token_count or task_complete line.
type codexUsageLine struct {
	Timestamp time.Time `json:"timestamp"`
	Payload   struct {
		Type       string `json:"type"`
		RateLimits *struct {
			LimitID   string       `json:"limit_id"`
			Primary   *codexWindow `json:"primary"`
			Secondary *codexWindow `json:"secondary"`
		} `json:"rate_limits"`
		Error *struct {
			CodexErrorInfo string `json:"codex_error_info"`
		} `json:"error"`
	} `json:"payload"`
}

// codexWindow is a window of a rate_limits snapshot; primary and secondary are slots,
// and window_minutes says which period it is.
type codexWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int64   `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"`
}

// Usage implements AiCodingAgent, from the agent's session file, where Codex writes
// the account's rate_limits beside each response's token_count: a snapshot of one
// bucket (limit_id) with two windows, each keyed by its period, as 10080m. The newest
// snapshot of codex, the bucket every model draws on, is read back to at most
// codexUsageReadLimit from the file's end, with the newest of each other bucket seen
// after it; a snapshot with no window, as premium's always, or a window of 0 minutes
// says nothing. At a limit Codex may stop at 99 % and refuse with a task_complete
// whose error is usage_limit_exceeded: one after the newest codex snapshot marks the
// codex rows limited, observed when it was written. The file is the one to follow.
// Checked on 0.142.3 to 0.167.0, as the session file's cli_version says (the wdx doc
// USAGE_CODEX).
func (Codex) Usage(l Launch) ([]Usage, []string, error) {
	file, err := codexSessionPath(l)
	if err != nil {
		return nil, nil, err
	}
	if file == "" {
		return nil, nil, ErrNoUsage
	}
	var limitedAt time.Time
	buckets := map[string][]Usage{}
	err = codexReadBack(file, codexUsageReadLimit, func(line []byte) bool {
		entry, ok := codexParseUsageLine(line)
		if !ok {
			return false
		}
		if e := entry.Payload.Error; e != nil && e.CodexErrorInfo == "usage_limit_exceeded" {
			if limitedAt.IsZero() {
				limitedAt = entry.Timestamp
			}
			return false
		}
		r := entry.Payload.RateLimits
		if r == nil {
			return false
		}
		bucket := r.LimitID
		if bucket == codexCommonBucket {
			bucket = ""
		}
		if _, seen := buckets[bucket]; seen {
			return false
		}
		rows := codexUsageRows(bucket, entry.Timestamp, r.Primary, r.Secondary)
		if len(rows) == 0 {
			return false
		}
		if bucket == "" && !limitedAt.IsZero() {
			for i := range rows {
				rows[i].Limited = true
				rows[i].ObservedAt = limitedAt
			}
		}
		buckets[bucket] = rows
		return bucket == ""
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, ErrNoUsage
	}
	if err != nil {
		return nil, []string{file}, err
	}
	if len(buckets) == 0 {
		return nil, []string{file}, ErrNoUsage
	}
	var rows []Usage
	for _, bucket := range slices.Sorted(maps.Keys(buckets)) {
		rows = append(rows, buckets[bucket]...)
	}
	return rows, []string{file}, nil
}

// codexParseUsageLine is a session line that may tell of the usage: a token_count or a
// task_complete. A line cut short, still being written, tells nothing.
func codexParseUsageLine(line []byte) (codexUsageLine, bool) {
	if !bytes.Contains(line, codexTokenCount) && !bytes.Contains(line, codexTaskComplete) {
		return codexUsageLine{}, false
	}
	var entry codexUsageLine
	if json.Unmarshal(line, &entry) != nil {
		return codexUsageLine{}, false
	}
	return entry, entry.Payload.Type == "token_count" || entry.Payload.Type == "task_complete"
}

// codexUsageRows is a row per window of a snapshot of bucket that has a period.
func codexUsageRows(bucket string, observed time.Time, windows ...*codexWindow) []Usage {
	var rows []Usage
	for _, w := range windows {
		if w == nil || w.WindowMinutes <= 0 {
			continue
		}
		row := Usage{
			Window:      fmt.Sprintf("%dm", w.WindowMinutes),
			Bucket:      bucket,
			UsedPercent: int(math.Round(w.UsedPercent)),
			ObservedAt:  observed,
		}
		if w.ResetsAt != 0 {
			row.ResetsAt = time.Unix(w.ResetsAt, 0)
		}
		rows = append(rows, row)
	}
	return rows
}

// codexModelBuckets is Codex's models, which all draw on the bucket "" alone: the
// other limit ids its session file names are no model's.
var codexModelBuckets = []modelFamily{{idPrefixes: []string{"gpt-"}, nameWords: []string{"GPT"}}}

// ModelBuckets implements AiCodingAgent, from codexModelBuckets.
func (Codex) ModelBuckets(id, name string) ([]string, bool) {
	return modelBuckets(codexModelBuckets, id, name)
}
