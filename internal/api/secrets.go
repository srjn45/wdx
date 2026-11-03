package api

import "encoding/json"

// WorktreeResponseLogged is a worktree response as logged, by the daemon and the
// cli: a file's contents and what is said of them can hold secrets, so a patch or
// content is measured and comments are counted, never logged, as a list of paths is
// counted for its length. b is the response's JSON.
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
	if content, ok := v["content"].(string); ok {
		delete(v, "content")
		v["content_bytes"] = len(content)
	}
	if paths, ok := v["paths"].([]any); ok {
		v["paths"] = len(paths)
	}
	if comments, ok := v["comments"].([]any); ok {
		v["comments"] = len(comments)
	}
}

// PlanResponseLogged is a response holding a plan's text as logged, by the daemon
// and the cli: the content is measured, and of the definition only the ids are
// kept, with the sizes of its goal and prompts and how many constraints it has, as
// they are what agents are told, which can hold secrets. b is the response's JSON.
func PlanResponseLogged(b []byte) any {
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		return map[string]any{"body_bytes": len(b)}
	}
	if content, ok := v["content"].(string); ok {
		delete(v, "content")
		v["content_bytes"] = len(content)
	}
	def, ok := v["definition"].(map[string]any)
	if !ok {
		return v
	}
	goal, _ := def["goal"].(string)
	constraints, _ := def["constraints"].([]any)
	logged := map[string]any{"goal_bytes": len(goal), "constraints": len(constraints), "tasks": def["tasks"], "plans": def["plans"]}
	if tasks, ok := def["tasks"].([]any); ok {
		list := make([]any, len(tasks))
		for i, t := range tasks {
			task, _ := t.(map[string]any)
			prompt, _ := task["prompt"].(string)
			list[i] = map[string]any{"id": task["id"], "after": task["after"], "prompt_bytes": len(prompt)}
		}
		logged["tasks"] = list
	}
	v["definition"] = logged
	return v
}
