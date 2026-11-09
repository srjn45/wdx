package aca

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Usage implements AiCodingAgent, from what Antigravity last gave its status line:
// every entry of quota, keyed by an id such as gemini-5h, whose last "-" part is the
// window and the rest the bucket, a group of models, as agy -p /usage states for every
// id it lists. No limit applies to every model, so no row has an empty bucket. The
// quota arrives about a second after start and is fetched again at most about once a
// minute while the agent works, so the file's time, taken as when it was told, may be
// up to a minute late. Checked on 1.5.3 (the wdx doc USAGE_ANTIGRAVITY).
func (Antigravity) Usage(l Launch) ([]Usage, []string, error) {
	if l.BackupDir == "" {
		return nil, nil, ErrNoUsage
	}
	input, err := parseStatusLine(l.BackupDir, antigravityStatusFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, ErrNoUsage
	}
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(filepath.Join(l.BackupDir, antigravityStatusFile))
	if err != nil {
		return nil, nil, err
	}
	rows := make([]Usage, 0, len(input.Quota))
	for _, id := range slices.Sorted(maps.Keys(input.Quota)) {
		quota := input.Quota[id]
		if quota.RemainingFraction == nil && quota.ResetTime == "" {
			continue
		}
		bucket, window := antigravityQuotaID(id)
		row := Usage{Window: window, Bucket: bucket, UsedPercent: 100, ObservedAt: info.ModTime()}
		if quota.RemainingFraction != nil {
			row.UsedPercent = antigravityUsedPercent(*quota.RemainingFraction)
		}
		if quota.ResetTime != "" {
			if row.ResetsAt, err = time.Parse(time.RFC3339, quota.ResetTime); err != nil {
				return nil, nil, fmt.Errorf("read %s: quota %s: %w", antigravityStatusFile, id, err)
			}
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, nil, ErrNoUsage
	}
	return rows, nil, nil
}

// antigravityQuotaID is the bucket and window of a quota id, split at its last "-":
// gemini-argon-wtu-weekly is gemini-argon-wtu's weekly. An id without "-" is both.
func antigravityQuotaID(id string) (bucket, window string) {
	i := strings.LastIndex(id, "-")
	if i < 0 {
		return id, id
	}
	return id[:i], id[i+1:]
}

// antigravityUsedPercent is the percentage used of a remaining fraction, rounded down,
// so that 100 is reached only when nothing remains: rounding to the nearest would
// show a bucket with 0.4 % left as used up while it still works. The small amount
// added keeps a fraction such as 0.34 from falling a whole point below, 65.999...
func antigravityUsedPercent(remaining float64) int {
	return int(math.Floor(100*(1-remaining) + 1e-9))
}

// antigravityModelBuckets is which group of models, as Usage names them, each model
// draws on, as agy -p /usage says in prose: Gemini 4 Argon on gemini-argon-wtu, the
// other Gemini models on gemini, Claude and GPT-OSS on 3p (the wdx doc
// USAGE_ANTIGRAVITY). Argon comes first, as its ids and names are Gemini's too.
var antigravityModelBuckets = []modelFamily{
	{idPrefixes: []string{"gemini-4-argon-"}, nameWords: []string{"Argon"}, buckets: []string{"gemini-argon-wtu"}},
	{idPrefixes: []string{"gemini-"}, nameWords: []string{"Gemini"}, buckets: []string{"gemini"}},
	{idPrefixes: []string{"claude-", "gpt-"}, nameWords: []string{"Claude", "GPT"}, buckets: []string{"3p"}},
}

// ModelBuckets implements AiCodingAgent: one group of antigravityModelBuckets, as no
// limit applies to every model.
func (Antigravity) ModelBuckets(id, name string) ([]string, bool) {
	return modelBuckets(antigravityModelBuckets, id, name)
}
