package cli

// The cli's test helpers on internal/clikit/clikittest, under the names its
// tests use. The callers task removes this file: the tests then use
// clikittest directly.

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/clikit/clikittest"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/identity"
)

type logLine = clikittest.LogLine

func (o output) kit() clikittest.Output {
	return clikittest.Output{Exit: o.exit, Stdout: o.stdout, Stderr: o.stderr, Pid: o.pid}
}

// wdxLogged is what a wdx run and its daemon log in a home.
func wdxLogged(home string) clikittest.Logged {
	return clikittest.Logged{Dir: identity.LogsDir(home), Called: "daemon called", Abandoned: "daemon unreachable", Served: "request returned"}
}

func codes(code string) (clikit.CodeInfo, bool) {
	info, ok := api.Codes[api.ErrorCode(code)]
	return info, ok
}

func logLines(t *testing.T, home string, args []string) []logLine {
	t.Helper()
	return clikittest.LogLines(t, identity.LogsDir(home), args)
}

func wantLogged(t *testing.T, home string, args []string, out output) {
	t.Helper()
	clikittest.WantLogged(t, wdxLogged(home), args, out.kit(), redacted(args, out))
}

// redacted turns what the run of args printed into what it logged: what wdx
// measures or counts rather than logs.
func redacted(args []string, out output) func(printed map[string]any) {
	return func(obj map[string]any) {
		replace := func(b []byte, err error) {
			if err != nil {
				return
			}
			clear(obj)
			_ = json.Unmarshal(b, &obj)
		}
		if (slices.Contains(args, "worktree") || slices.Contains(args, "message")) && out.exit == clikit.ExitOK {
			// A file's contents and comments are measured, not logged.
			replace(json.Marshal(api.WorktreeResponseLogged([]byte(out.stdout))))
		}
		if obj["agent"] != nil || obj["agents"] != nil || obj["project"] != nil {
			// A prompt's text is the agent's: its kind and options are counted, not logged.
			replace(json.Marshal(api.AgentResponseLogged([]byte(out.stdout))))
		}
		if h, ok := obj["history"].(map[string]any); ok {
			h["lines"] = float64(len(h["lines"].([]any))) // terminal output is counted, not logged
		}
		if r, ok := obj["transcript"].(map[string]any); ok {
			r["lines"] = float64(len(r["lines"].([]any))) // a conversation is counted, not logged
		}
		if content, ok := obj["content"].(string); ok && obj["doc"] != nil {
			delete(obj, "content") // a doc is measured, not logged
			obj["content_bytes"] = float64(len(content))
		}
		if obj["plan"] != nil && (obj["content"] != nil || obj["definition"] != nil) {
			// A plan's text is measured, not logged.
			replace(json.Marshal(api.PlanResponseLogged([]byte(out.stdout))))
		}
		if obj["task"] != nil && obj["definition"] == nil {
			// A task's prompt and summary are measured, not logged.
			replace(json.Marshal(api.PlanTaskResponseLogged([]byte(out.stdout))))
		}
	}
}

func wantDeclared(t *testing.T, args []string, v jsonOutput) {
	t.Helper()
	var conflict string
	if v.Conflict != nil {
		conflict = v.Conflict.Code
	}
	clikittest.WantDeclared(t, newRoot(config.Default()), args, v.Result, v.Code, conflict)
}

func typed(t *testing.T, answers ...string) *os.File {
	t.Helper()
	return clikittest.Typed(t, answers...)
}

func checkCommandLine(line string) (*cobra.Command, error) {
	return clikittest.CheckCommandLine(func() *cobra.Command { return newRoot(config.Default()) }, line)
}

func parseExamples(example string) []clikit.Example { return clikit.ParseExamples(example) }

func flagCompletion(cmd *cobra.Command, name string) (cobra.CompletionFunc, bool) {
	return clikit.FlagCompletion(cmd, name)
}

func flagRules(cmd *cobra.Command) []clikit.FlagRule { return clikit.FlagRules(cmd) }

func annotationList(cmd *cobra.Command, key string) []string { return clikit.AnnotationList(cmd, key) }

type completionScript struct {
	Shell  string `json:"shell"`
	Script string `json:"script"`
}
