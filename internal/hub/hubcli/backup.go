package hubcli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/style"
)

func newBackupCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "backup --out <path>",
		Short: "Copy the hub's database and certificate authority while it runs",
		Long: "The running hub writes a consistent copy of its database to the path, and its\n" +
			"certificate authority to the directory <path>.ca beside it, neither of which\n" +
			"may exist, on the filesystem it runs on: in a container, the container's. The\n" +
			"directory holds the certificate authority's private key: keep the backup as\n" +
			"safe as the master key, which it does not hold.",
		Example: clikit.Examples(
			"Back up the database", name+" backup --out /backups/hub.db",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not backed up",
			clikit.OutputNote: "The paths of the copy and of the certificate authority's.\n" +
				`With --json: {"result": "backed_up", "path": ..., "ca_path": ...}`,
			clikit.Results: clikit.Annotation(string(hubapi.ResultBackedUp)),
			clikit.Errors:  clikit.CodeList(hubapi.ErrorCodeHubUnavailable, hubapi.ErrorCodePathExists),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := clikit.Absolute(cmd, out)
			if err != nil {
				return err
			}
			r, err := clientOf(cmd).backup(cmd.Context(), path)
			if err != nil {
				return err
			}
			return clikit.PrintResult(cmd, backupResult(r))
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "`path` of the copy, which must not exist")
	cmd.MarkFlagRequired("out")
	clikit.CompleteFlag(cmd, "out", clikit.CompleteFiles)
	return cmd
}

type backupResult hubapi.BackupResult

func (r backupResult) Text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "%s the database to %s and the certificate authority to %s\n", st.Success("backed up"), st.Name(r.Path), st.Name(r.CaPath))
}

// topics are wdx-hub's help topics.
var topics = []clikit.Topic{
	{Name: "settings", Summary: "every setting, its default and where it is read from", Text: settingsTopic, Settings: hubconfig.Settings()},
	{Name: "logs", Summary: "where serve and the other commands log, and finding a run by its debug_id", Text: logsTopic},
}

func settingsTopic(st style.Style) string {
	var b strings.Builder
	b.WriteString(`Read when ` + name + ` starts; an invalid one fails every command. The home is read
from ` + hubconfig.EnvPrefix + `HOME, the rest from <home>/config.yaml, where log.level is written
as "log: {level: debug}", and from ` + hubconfig.EnvPrefix + `<KEY>, as ` + hubconfig.EnvPrefix + `LOG_LEVEL, which wins
over the file. ` + st.Command(name+" setting list") + ` shows them, and ` + st.Command(name+" setting set") + ` changes one
and has the running hub read the file again. ` + st.Command(name+" setting write") + ` lists every
setting in the file.

`)
	b.WriteString(clikit.SettingsTable(st, hubconfig.Settings()))
	return strings.TrimRight(b.String(), "\n")
}

func logsTopic(st style.Style) string {
	return st.Command(name+" serve") + ` logs JSON lines to stdout, which a container or a service
manager keeps. Every other command logs to <home>/logs/` + name + `-<date>.log, kept for
log.retention_days days.

A failure prints its debug_id; every line of that run, in the command and in
the hub's log, carries it.`
}
