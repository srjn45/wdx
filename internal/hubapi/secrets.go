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

// The fields of the node logins' requests and responses that are secrets:
// whoever has one of them can finish the login, and so make a node of the
// account. Each is measured, never logged, by the hub and by the node.
var (
	// requestSecrets are the secret fields of a request's body, by
	// operation id.
	requestSecrets = map[string][]string{
		"exchangeNodeLogin": {"code", "pkce_verifier"},
		"pollNodeLogin":     {"device_code"},
	}
	// responseSecrets are the secret fields of a response's body, by
	// operation id.
	responseSecrets = map[string][]string{
		"startNodeLogin": {"device_code", "user_code"},
	}
)

// RequestLogged is the body b of a request of operation as logged: its
// secret fields measured, the others as they are; nil when b is not JSON.
func RequestLogged(operation string, b []byte) any {
	return measured(b, requestSecrets[operation])
}

// ResponseLogged is the body b of a response of operation as logged: its
// secret fields measured, the others as they are.
func ResponseLogged(operation string, b []byte) any {
	return measured(b, responseSecrets[operation])
}

// HasSecrets reports whether a request or a response of operation holds a
// secret field.
func HasSecrets(operation string) bool {
	return requestSecrets[operation] != nil || responseSecrets[operation] != nil
}

// measured is the JSON object b with each of keys that holds a string
// replaced by <key>_bytes, its length. A body that is no object is measured
// whole when keys has any, since it cannot be told what it holds.
func measured(b []byte, keys []string) any {
	if len(keys) == 0 {
		if json.Valid(b) {
			return json.RawMessage(b)
		}
		return string(b)
	}
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		return map[string]any{"body_bytes": len(b)}
	}
	for _, key := range keys {
		if s, ok := v[key].(string); ok {
			delete(v, key)
			v[key+"_bytes"] = len(s)
		}
	}
	return v
}
