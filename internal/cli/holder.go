package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

func newHolderCmd() *cobra.Command {
	var o holder.Options
	var acaID string
	cmd := &cobra.Command{
		Use:    "holder --id <id> --project <name> --dir <dir> --shell <path> [--aca <id>] [--command <word>]... [--history-lines <lines>] [--write-timeout <duration>]",
		Short:  "Hold a terminal's PTY and shell; the daemon runs it",
		Hidden: true,
		Args:   cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "Nothing: it reports to the daemon on fd 3 and on its socket.",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			env := invocationOf(cmd).env
			o.Home = env.Config.Home
			o.Socket = identity.HolderSocket(o.Home, o.ID)
			// The daemon starts it with exactly the variables the shell gets.
			o.Env = env.ShellEnv
			o.Ready = os.NewFile(3, "ready")
			ctx := logging.NewContext(cmd.Context(), logging.FromContext(cmd.Context()).With("terminal_id", o.ID))
			if acaID != "" {
				// A screen it cannot read must not stop the terminal: this binary
				// may be older than the daemon that named the ACA.
				if known, ok := aca.ByID(acaID); ok {
					o.ParseScreen = known.ScreenParser().Parse
					o.Deliver = known.Deliver
					o.Interrupt = known.Interrupt()
				} else {
					logging.FromContext(ctx).Warn("screen not read: this holder does not know the aca", "aca", acaID)
				}
			}
			return holder.Run(ctx, o)
		},
	}
	cmd.Flags().StringVar(&o.ID, "id", "", "`id` of the terminal")
	cmd.Flags().StringVar(&o.Project, "project", "", "`name` of its project")
	cmd.Flags().StringVar(&o.Dir, "dir", "", "`dir` the shell starts in")
	cmd.Flags().StringVar(&o.Shell, "shell", "", "`path` of the shell")
	cmd.Flags().StringVar(&o.AgentID, "agent-id", "", "`id` of the agent the terminal runs, if any")
	cmd.Flags().StringVar(&acaID, "aca", "", "`id` of the AI coding agent it runs, which says how to read its screen and give it messages")
	cmd.Flags().StringArrayVar(&o.Command, "command", nil, "`word` of the command to run through the shell, in place of a shell to type in; one flag per word")
	cmd.Flags().Int64Var(&o.HistoryLines, "history-lines", 0, "`lines` of history kept; 0 keeps none")
	cmd.Flags().DurationVar(&o.WriteTimeout, "write-timeout", 0, "`duration` a write to a peer may take; 0 for the default")
	for _, name := range strings.Fields("id project dir shell") {
		cmd.MarkFlagRequired(name)
	}
	return cmd
}
