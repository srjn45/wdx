package tide

import (
	"cmp"
	"strconv"

	"github.com/srjn45/wdx/internal/api"
)

// An agent's context size shows in its row and its info tab, colored by the
// context.threshold settings (the wdx doc CONTEXT_SIZE).

// The settings' keys, and their defaults, which internal/config repeats.
const (
	contextWarningKey      = "context.threshold.warning"
	contextCriticalKey     = "context.threshold.critical"
	defaultContextWarning  = 200_000
	defaultContextCritical = 400_000
)

// contextThresholds are the tokens in a context from which its size is a warning,
// and critical.
type contextThresholds struct{ warning, critical int64 }

// newContextThresholds returns the thresholds given, the defaults for those not.
func newContextThresholds(warning, critical int64) contextThresholds {
	return contextThresholds{warning: cmp.Or(warning, defaultContextWarning), critical: cmp.Or(critical, defaultContextCritical)}
}

// set takes a threshold saved in the settings tab, valid as the daemon took it;
// reports whether key is one.
func (t *contextThresholds) set(key, value string) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	switch {
	case err != nil:
		return false
	case key == contextWarningKey:
		t.warning = n
	case key == contextCriticalKey:
		t.critical = n
	default:
		return false
	}
	return true
}

// contextSize is the tokens in a's context, "" before its AI coding agent told.
func contextSize(a api.Agent) string {
	if a.Context == nil {
		return ""
	}
	return api.TokenCount(a.Context.Tokens)
}

// contextPaint styles the lines telling the size of a's context in the color its
// tokens call for; nil before its AI coding agent told.
func (m *Model) contextPaint(a api.Agent) func(string) string {
	if a.Context == nil {
		return nil
	}
	return func(text string) string {
		return m.st.Context(a.Context.Tokens, m.thresholds.warning, m.thresholds.critical, text)
	}
}
