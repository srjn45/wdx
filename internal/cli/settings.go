package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/style"
)

func newSettingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Manage the config file",
		Long:  identity.Name + " help settings lists every setting.",
		Args:  noArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newSettingsWriteCmd())
	return cmd
}

func newSettingsWriteCmd() *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "write [--to <path>]",
		Short: "Write the config file anew, listing every setting of this version",
		Long: "Each setting is listed with its description: the values set are kept as\n" +
			"written, the others are commented with their defaults. Keys no setting has\n" +
			"are moved, commented, to the end of the file. Comments of your own are not\n" +
			"kept. " + identity.Name + " update does the same with the settings of the release it installs.",
		Example: examples(
			"Add the settings of this version to the config file", identity.Name+" settings write",
			"Write the result to another file, leaving the config file alone", identity.Name+" settings write --to /tmp/config.yaml",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The file written, the settings it lists that the file did not and the keys moved to its end.\n" +
				`With --json: {"result": ..., "settings": {"path": ..., "added": [...], "dropped": [...]}}`,
			resultList: annotation("unchanged", "written"),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			log := logging.FromContext(cmd.Context())
			path := identity.ConfigPath(invocationOf(cmd).env.Config.Home)
			dest, err := absolute(cmd, to)
			if err != nil {
				return err
			}
			r, err := config.Rewrite(path)
			if err != nil {
				return err
			}
			result := "unchanged"
			if dest == "" {
				dest = path
			}
			if r.Changed || dest != path {
				if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
					return err
				}
				if err := config.WriteFile(dest, r.Content); err != nil {
					return err
				}
				result = "written"
			}
			log.Info("settings "+result, "from", path, "path", dest, "added", r.Added, "dropped", r.Dropped)
			return printResult(cmd, settingsResult{Result: result, Settings: settingsChange{Path: dest, Added: r.Added, Dropped: r.Dropped}})
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "write it to `path` instead of the config file")
	cmd.RegisterFlagCompletionFunc("to", completeFiles)
	return cmd
}

// RewritesSettings is true when args run settings write and err names only
// keys no setting has: that command moves them out of the way, so it runs
// on the settings that loaded.
func RewritesSettings(args []string, err error) bool {
	e, ok := errors.AsType[*config.Error](err)
	if !ok || !e.OnlyUnknown() {
		return false
	}
	cmd, _, err := newRoot(config.Default()).Find(args)
	return err == nil && cmd.CommandPath() == identity.Name+" settings write"
}

// settingsResult is what settings write did.
type settingsResult struct {
	Result   string         `json:"result"`
	Settings settingsChange `json:"settings"`
}

type settingsChange struct {
	Path    string   `json:"path"`
	Added   []string `json:"added"`
	Dropped []string `json:"dropped"`
}

func (r settingsResult) text(w io.Writer, st style.Style) {
	s := r.Settings
	if r.Result == "unchanged" {
		fmt.Fprintf(w, "%s: %s already lists every setting\n", st.Success("up to date"), st.Name(s.Path))
	} else {
		fmt.Fprintf(w, "%s %s\n", st.Success("wrote"), st.Name(s.Path))
	}
	if len(s.Added) > 0 {
		fmt.Fprintf(w, "  %s %s\n", st.Muted("added:  "), strings.Join(s.Added, ", "))
	}
	if len(s.Dropped) > 0 {
		fmt.Fprintf(w, "  %s %s %s\n", st.Muted("dropped:"), strings.Join(s.Dropped, ", "), st.Muted("(commented at the end)"))
	}
}

// settingsProblem is the problem of settings that failed to load or to be
// written anew.
func settingsProblem(e *config.Error) api.Problem {
	p := api.NewProblem(api.ErrorCodeUsage, e.Error())
	hint := "fix or remove them"
	if e.OnlyUnknown() {
		hint = "run " + identity.Name + " settings write, which comments them out at the end of the file"
	}
	p.Hint = &hint
	p.Details = &map[string]any{"settings": e.Problems}
	return p
}
