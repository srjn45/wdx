package api

import "encoding/json"

// WorktreeResponseLogged is a worktree response as logged, by the daemon and the
// cli: a file's contents and what is said of them can hold secrets, so a patch is
// measured and comments are counted, never logged. b is the response's JSON.
func WorktreeResponseLogged(b []byte) any {
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		return map[string]any{"body_bytes": len(b)}
	}
	worktreeSecretsOut(v)
	if list, ok := v["worktrees"].([]any); ok {
		for _, w := range list {
			if m, ok := w.(map[string]any); ok {
				worktreeSecretsOut(m)
			}
		}
	}
	if list, ok := v["messages"].([]any); ok {
		v["messages"] = len(list)
	}
	if m, ok := v["message"].(map[string]any); ok {
		v["message"] = map[string]any{"id": m["id"], "state": m["state"]}
	}
	if c, ok := v["comment"].(map[string]any); ok {
		v["comment"] = map[string]any{"id": c["id"], "file": c["file"], "line": c["line"], "state": c["state"]}
	}
	return v
}

func worktreeSecretsOut(v map[string]any) {
	if patch, ok := v["patch"].(string); ok {
		delete(v, "patch")
		v["patch_bytes"] = len(patch)
	}
	if comments, ok := v["comments"].([]any); ok {
		v["comments"] = len(comments)
	}
}
