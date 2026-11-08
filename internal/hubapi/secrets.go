package hubapi

import "encoding/json"

// LinkResponseLogged is a response holding an invite or a reset as logged, by
// the hub and its commands: the link holds a token that lets whoever has it
// make an account or take one over, so it is measured, never logged. b is the
// response's JSON.
func LinkResponseLogged(b []byte) any {
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		return map[string]any{"body_bytes": len(b)}
	}
	for _, key := range []string{"invite", "reset"} {
		if m, ok := v[key].(map[string]any); ok {
			if link, ok := m["link"].(string); ok {
				delete(m, "link")
				m["link_bytes"] = len(link)
			}
		}
	}
	return v
}
