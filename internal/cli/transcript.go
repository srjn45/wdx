package cli

import (
	"fmt"
	"io"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

func newAgentTranscriptCmd() *cobra.Command {
	var ref agentRef
	var tail, count int
	var from int64
	var path, brief bool
	cmd := &cobra.Command{
		Use:   "transcript (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--tail <lines> | [--from <line>] [--count <lines>] | --path | --brief]",
		Short: "Print an agent's conversation as text",
		Long: "wdx keeps its own copy of the conversation, which only grows: it goes on across\n" +
			"compactions and clears, and stays after the AI coding agent deletes its own, until\n" +
			"the agent is removed and for 7 days after that. Unlike the terminal's history,\n" +
			"it is what was said and done, not what the screen showed. It can hold whatever\n" +
			"the agent's tools read. --path prints the file's path, for grep. Lines are numbered\n" +
			"from 0, which --from counts in; --count alone starts at the first.\n\n" +
			"--brief is for an agent in a new session: from the latest summary, or the first\n" +
			"request, it prints the messages since, and each tool call as one line, with the\n" +
			"line of the file that --from reads it at. It goes no further back than a /clear.",
		Example: clikit.Examples(
			"The last lines of an agent of the project you are in", identity.Name+" agent transcript --name reviewer",
			"The last 50", identity.Name+" agent transcript --name reviewer --tail 50",
			"The file, to grep it", identity.Name+" agent transcript --name reviewer --path",
			"The conversation in brief", identity.Name+" agent transcript --id agent-1a2b3c4d --brief",
			"From line 1200 on, as JSON", identity.Name+" agent transcript --id agent-1a2b3c4d --from 1200 --count 100 --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "The lines, oldest first, as Markdown, or the file's path with --path, or the brief.\n" + `With --json: {"transcript": {"path": ..., "from": ..., "next": ..., "lines": [...]}}`,
			clikit.Errors:     clikit.CodeList(api.ErrorCodeNotFound, api.ErrorCodeAcaUnsupported, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			ref2, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			if brief {
				r, err := clientOf(cmd).GetAgentBrief(cmd.Context(), ref2, project, dir)
				if err != nil {
					return agentHint(ref.notFoundHint(err))
				}
				return clikit.PrintResult(cmd, transcriptResult{r, false})
			}
			n := 1000
			var start *int64
			switch {
			case cmd.Flags().Changed("from") || cmd.Flags().Changed("count"):
				start, n = &from, count
			case cmd.Flags().Changed("tail"):
				n = tail
			case path:
				n = 1
			}
			if n < 1 || n > 10_000 {
				return &clikit.UsageError{Err: fmt.Errorf("%d lines is not between 1 and 10000", n)}
			}
			r, err := clientOf(cmd).GetAgentTranscript(cmd.Context(), ref2, project, dir, start, n)
			if err != nil {
				return agentHint(ref.notFoundHint(err))
			}
			return clikit.PrintResult(cmd, transcriptResult{r, path})
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().IntVar(&tail, "tail", 1000, "the last `lines`")
	cmd.Flags().Int64Var(&from, "from", 0, "number of the first `line`")
	cmd.Flags().IntVar(&count, "count", 1000, "`lines` from --from on, or from the first")
	cmd.Flags().BoolVar(&path, "path", false, "print the file's path, not its lines")
	cmd.Flags().BoolVar(&brief, "brief", false, "print the conversation in brief, for an agent to read")
	cmd.MarkFlagsMutuallyExclusive("brief", "tail")
	cmd.MarkFlagsMutuallyExclusive("brief", "from")
	cmd.MarkFlagsMutuallyExclusive("brief", "count")
	cmd.MarkFlagsMutuallyExclusive("brief", "path")
	cmd.MarkFlagsMutuallyExclusive("tail", "from")
	cmd.MarkFlagsMutuallyExclusive("tail", "count")
	cmd.MarkFlagsMutuallyExclusive("path", "tail")
	cmd.MarkFlagsMutuallyExclusive("path", "from")
	cmd.MarkFlagsMutuallyExclusive("path", "count")
	for _, name := range []string{"tail", "from", "count"} {
		clikit.CompleteFlag(cmd, name, cobra.NoFileCompletions)
	}
	return cmd
}

// transcriptResult is lines of an agent's conversation, or only where they are.
type transcriptResult struct {
	api.AgentTranscriptEnvelope
	path bool
}

func (r transcriptResult) Text(w io.Writer, _ style.Style) {
	if r.path {
		fmt.Fprintln(w, r.Transcript.Path)
		return
	}
	for _, line := range r.Transcript.Lines {
		fmt.Fprintln(w, line)
	}
}

// LogValue keeps the lines out of the log: a conversation can hold secrets.
func (r transcriptResult) LogValue() slog.Value {
	t := r.Transcript
	return slog.GroupValue(slog.Group("transcript",
		"path", t.Path, "from", t.From, "next", t.Next, "lines", len(t.Lines)))
}
