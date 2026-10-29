package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

func newHolderCmd() *cobra.Command {
	var o holder.Options
	cmd := &cobra.Command{
		Use:    "holder --id <id> --project <name> --dir <dir> --shell <path> [--history-lines <lines>]",
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
			o.Env = env.Terminal
			o.Ready = os.NewFile(3, "ready")
			ctx := logging.NewContext(cmd.Context(), logging.FromContext(cmd.Context()).With("terminal_id", o.ID))
			return holder.Run(ctx, o)
		},
	}
	cmd.Flags().StringVar(&o.ID, "id", "", "`id` of the terminal")
	cmd.Flags().StringVar(&o.Project, "project", "", "`name` of its project")
	cmd.Flags().StringVar(&o.Dir, "dir", "", "`dir` the shell starts in")
	cmd.Flags().StringVar(&o.Shell, "shell", "", "`path` of the shell")
	cmd.Flags().Int64Var(&o.HistoryLines, "history-lines", 0, "`lines` of history kept; 0 keeps none")
	for _, name := range strings.Fields("id project dir shell") {
		cmd.MarkFlagRequired(name)
	}
	return cmd
}
