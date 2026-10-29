package cli

import (
	"fmt"
	"io"
	"log/slog"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

func newTerminalHistoryCmd() *cobra.Command {
	var ref terminalRef
	var tail, count int
	var from int64
	var plain bool
	cmd := &cobra.Command{
		Use:   "history (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--tail <lines> | --from <line> [--count <lines>]] [--plain]",
		Short: "Print the lines that scrolled off a terminal's screen",
		Long: "History is kept on disk for the terminal's life, across restarts of its shell,\n" +
			"up to terminal.history_lines, and is there in any state. Lines keep their\n" +
			"colors when the output gets color; --plain, or output that is not a terminal,\n" +
			"strips them. Lines are numbered from 0, which --from counts in.",
		Example: examples(
			"The last lines of a terminal of the project you are in", identity.Name+" terminal history --name server",
			"The last 50, without colors", identity.Name+" terminal history --name server --tail 50 --plain",
			"From line 1200 on, as JSON", identity.Name+" terminal history --id term-1a2b3c4d --from 1200 --count 100 --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The lines, oldest first.\n" + `With --json: {"history": {"from": ..., "next": ..., "first": ..., "lines": [...]}}`,
			errorList:  codeList(api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			n := invocationOf(cmd).env.Config.TerminalHistoryTail
			var start *int64
			switch {
			case cmd.Flags().Changed("from"):
				start, n = &from, 1000
				if cmd.Flags().Changed("count") {
					n = count
				}
			case cmd.Flags().Changed("tail"):
				n = tail
			}
			if n < 1 || n > 10_000 {
				return &usageError{fmt.Errorf("%d lines is not between 1 and 10000", n)}
			}
			h, err := clientOf(cmd).GetTerminalHistory(cmd.Context(), path, project, dir, start, n)
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, historyResult{h, plain})
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().IntVar(&tail, "tail", 0, "the last `lines` (default: terminal.history_tail)")
	cmd.Flags().Int64Var(&from, "from", 0, "number of the first `line`")
	cmd.Flags().IntVar(&count, "count", 1000, "`lines` from --from on")
	cmd.Flags().BoolVar(&plain, "plain", false, "strip colors and other escape codes")
	cmd.MarkFlagsMutuallyExclusive("tail", "from")
	cmd.MarkFlagsMutuallyExclusive("tail", "count")
	for _, name := range []string{"tail", "from", "count"} {
		cmd.RegisterFlagCompletionFunc(name, cobra.NoFileCompletions)
	}
	return cmd
}

// historyResult is lines of a terminal's history.
type historyResult struct {
	api.TerminalHistoryEnvelope
	plain bool
}

func (r historyResult) text(w io.Writer, st style.Style) {
	for _, line := range r.History.Lines {
		if r.plain || !st.On() {
			line = ansi.Strip(line)
		}
		fmt.Fprintln(w, line)
	}
}

// LogValue keeps the lines out of the log: terminal output can hold
// secrets.
func (r historyResult) LogValue() slog.Value {
	h := r.History
	return slog.GroupValue(slog.Group("history",
		"from", h.From, "next", h.Next, "first", h.First, "lines", len(h.Lines)))
}
