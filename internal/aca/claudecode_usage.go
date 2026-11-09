package aca

// Usage implements AiCodingAgent, from what Claude Code last gave its status line:
// every window of rate_limits, five_hour and seven_day on a claude.ai plan, spend_limit
// behind a gateway, each with used_percentage and resets_at as given, none of them
// naming a bucket. The block is there only on a Pro, Max or Ultra plan, after the first
// API response, and Claude Code drops a window once its resets_at passes. Checked on
// 2.1.319 (the wdx doc USAGE).
func (ClaudeCode) Usage(l Launch) ([]Usage, []string, error) {
	if l.BackupDir == "" {
		return nil, nil, ErrNoUsage
	}
	rows, err := readStatusUsage(l.BackupDir, claudeCodeStatusFile)
	return rows, nil, err
}

// claudeCodeModelBuckets is Claude Code's models, its aliases (opus takes opusplan and
// opus[1m] too) and full ids, which all draw on the bucket "" alone, as its status line
// names no other; the Fable weekly of /usage, once read, is Fable's (the wdx doc
// AGENT_LIMITED).
var claudeCodeModelBuckets = []modelFamily{{
	idPrefixes: []string{"fable", "opus", "sonnet", "haiku", "claude-"},
	nameWords:  []string{"Fable", "Opus", "Sonnet", "Haiku", "Claude"},
}}

// ModelBuckets implements AiCodingAgent, from claudeCodeModelBuckets.
func (ClaudeCode) ModelBuckets(id, name string) ([]string, bool) {
	return modelBuckets(claudeCodeModelBuckets, id, name)
}
