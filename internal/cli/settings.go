package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/style"
)

func newSettingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "setting",
		// Every installed wdx runs the release it updates to as settings
		// write, so this name stays for good: docs/SETTINGS.md.
		Aliases: []string{"settings"},
		Short:   "Show and change the settings",
		Long:    identity.Name + " help settings explains every setting.",
		Args:    noArgs,
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newSettingListCmd(), newSettingGetCmd(), newSettingSetCmd(), newSettingWriteCmd())
	return cmd
}

func newSettingListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every setting with its value",
		Example: examples(
			"List the settings", identity.Name+" setting list",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "Each setting's key, value and where the value comes from.\n" +
				`With --json: {"settings": [{"key": ..., "value": ..., "default": ..., "description": ..., "from": ...}, ...]}`,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := invocationOf(cmd).env.Config
			list := settingList{Settings: []settingValue{}}
			for _, s := range config.Settings() {
				list.Settings = append(list.Settings, viewOf(cfg, s))
			}
			return printResult(cmd, list)
		},
	}
}

func newSettingGetCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "get --name <key>",
		Short: "Show one setting",
		Example: examples(
			"Show one setting", identity.Name+" setting get --name log.level",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The setting's value, default, description and where the value comes from.\n" +
				`With --json: {"setting": {"key": ..., "value": ..., "default": ..., "description": ..., "from": ...}}`,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := findSetting(name)
			if err != nil {
				return err
			}
			return printResult(cmd, settingEnvelope{viewOf(invocationOf(cmd).env.Config, s)})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "`key` of the setting, such as log.level")
	cmd.MarkFlagRequired("name")
	cmd.RegisterFlagCompletionFunc("name", completeSettings(false))
	return cmd
}

func newSettingSetCmd() *cobra.Command {
	var name, value string
	cmd := &cobra.Command{
		Use:   "set --name <key> --value <value>",
		Short: "Change a setting and apply it at once",
		Long: "The value is checked, saved in the config file, then applied: the daemon\n" +
			"reads the file again, and every later command reads it anyway. Terminals\n" +
			"already running keep their shell and history limit, and a running tide its\n" +
			"colors. The file is written anew, as by " + identity.Name + " setting write, so comments\n" +
			"of your own are not kept.",
		Example: examples(
			"Log in more detail", identity.Name+" setting set --name log.level --value debug",
			"Follow the dev releases", identity.Name+" setting set --name update.channel --value dev",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not set",
			outputNote: "The setting, and what the daemon did with it: applied, not_running (it reads the\n" +
				"value when it starts) or not_applied (saved, with a warning on stderr to restart it).\n" +
				`With --json: {"result": ..., "setting": {...}, "reload": ..., "dropped_settings": [...]}`,
			resultList: annotation(resultUpdated, resultUnchanged),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			log := logging.FromContext(cmd.Context())
			s, err := findSetting(name)
			if err != nil {
				return err
			}
			if s.EnvVar != "" {
				return &hinted{&usageError{fmt.Errorf("%s is read from %s, not the config file", s.Key, s.EnvVar)}, "set " + s.EnvVar + " in the environment"}
			}
			home := invocationOf(cmd).env.Config.Home
			r, err := config.Set(home, s.Key, value)
			if _, invalid := errors.AsType[*config.Error](err); invalid {
				return &hinted{err, "see what it takes in " + identity.Name + " help settings"}
			}
			if err != nil {
				return err
			}
			result := resultUnchanged
			if r.Changed {
				result = resultUpdated
			}
			log.Info("setting "+result, "name", s.Key, "value", value, "path", identity.ConfigPath(home), "dropped", r.Dropped)
			// What was just written loads: Rewrite checked it and moved away
			// the keys no setting has.
			cfg, err := config.LoadHome(home)
			if err != nil {
				return err
			}
			return printResult(cmd, settingSetResult{Result: result, Setting: viewOf(cfg, s), Reload: reloadDaemon(cmd), DroppedSettings: r.Dropped})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "`key` of the setting, such as log.level")
	cmd.Flags().StringVar(&value, "value", "", "the new `value`")
	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("value")
	cmd.RegisterFlagCompletionFunc("name", completeSettings(true))
	cmd.RegisterFlagCompletionFunc("value", cobra.NoFileCompletions)
	return cmd
}

// reloadDaemon has the daemon apply the config file and says what came of
// it. The file is saved by now, so a daemon that does not take it, as an
// older one still running while wdx is installed, only gets a warning.
func reloadDaemon(cmd *cobra.Command) string {
	err := clientOf(cmd).ReloadDaemon(cmd.Context())
	switch {
	case err == nil:
		return reloadApplied
	case unavailable(err):
		return resultNotRunning
	}
	logging.FromContext(cmd.Context()).Warn("daemon did not apply the settings", "err", err)
	st := stderrStyle(cmd)
	fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s saved, but the daemon did not apply it: %v; run %s\n",
		identity.Name, st.Warning("warning:"), err, st.Command(identity.Name+" daemon restart"))
	return reloadNotApplied
}

func newSettingWriteCmd() *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "write [--to <path>]",
		Short: "Write the config file anew, listing every setting of this version",
		Long: "Each setting is listed with its description: the values set are kept as\n" +
			"written, the others are commented with their defaults. Keys no setting has\n" +
			"are moved, commented, to the end of the file. Comments of your own are not\n" +
			"kept. " + identity.Name + " update does the same with the settings of the release it installs.",
		Example: examples(
			"Add the settings of this version to the config file", identity.Name+" setting write",
			"Write the result to another file, leaving the config file alone", identity.Name+" setting write --to /tmp/config.yaml",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not written",
			outputNote: "The file written, the settings it lists that the file did not and the keys moved to its end.\n" +
				`With --json: {"result": ..., "settings": {"path": ..., "added": [...], "dropped": [...]}}`,
			resultList: annotation(resultUnchanged, resultWritten),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			log := logging.FromContext(cmd.Context())
			home := invocationOf(cmd).env.Config.Home
			path := identity.ConfigPath(home)
			dest, err := absolute(cmd, to)
			if err != nil {
				return err
			}
			if dest == "" {
				dest = path
			}
			if dest == path {
				if err := os.MkdirAll(home, 0o700); err != nil {
					return err
				}
				unlock, err := config.Lock(home)
				if err != nil {
					return err
				}
				defer unlock()
			}
			r, err := config.Rewrite(path, nil)
			if err != nil {
				return err
			}
			result := resultUnchanged
			if r.Changed || dest != path {
				if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
					return err
				}
				if err := config.WriteFile(dest, r.Content); err != nil {
					return err
				}
				result = resultWritten
			}
			log.Info("settings "+result, "from", path, "path", dest, "added", r.Added, "dropped", r.Dropped)
			return printResult(cmd, settingsResult{Result: result, Settings: settingsChange{Path: dest, Added: r.Added, Dropped: r.Dropped}})
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "write it to `path` instead of the config file")
	cmd.RegisterFlagCompletionFunc("to", completeFiles)
	return cmd
}

// findSetting returns the setting named key.
func findSetting(key string) (config.Setting, error) {
	list := config.Settings()
	if i := slices.IndexFunc(list, func(s config.Setting) bool { return s.Key == key }); i >= 0 {
		return list[i], nil
	}
	return config.Setting{}, &hinted{&usageError{fmt.Errorf("no setting is named %q", key)}, "see every setting with " + identity.Name + " setting list"}
}

// settingValue is a setting as the commands show it.
type settingValue struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Default     string `json:"default"`
	Description string `json:"description"`
	From        string `json:"from"`
}

func viewOf(cfg config.Config, s config.Setting) settingValue {
	return settingValue{Key: s.Key, Value: cfg.Value(s.Key), Default: s.Default, Description: s.Description, From: cfg.From[s.Key]}
}

// shown is a value as text shows it, where an empty one would not show.
func shown(value string) string {
	return cmp.Or(value, `""`)
}

type settingList struct {
	Settings []settingValue `json:"settings"`
}

func (l settingList) text(w io.Writer, st style.Style) {
	var table style.Table
	for _, s := range l.Settings {
		table.Row(st.Name(s.Key), shown(s.Value), st.Muted(s.From))
	}
	table.Write(w)
}

type settingEnvelope struct {
	Setting settingValue `json:"setting"`
}

func (e settingEnvelope) text(w io.Writer, st style.Style) {
	s := e.Setting
	fmt.Fprintf(w, "%s %s\n", st.Name(s.Key), shown(s.Value))
	field := fieldPrinter(w, st, len("description"))
	field("description", s.Description)
	field("default", shown(s.Default))
	field("from", s.From)
}

// settingSetResult is what setting set did.
type settingSetResult struct {
	Result  string       `json:"result"`
	Setting settingValue `json:"setting"`
	// Reload is what the daemon did: reloadApplied, resultNotRunning or
	// reloadNotApplied.
	Reload string `json:"reload"`
	// DroppedSettings are the keys no setting has that the rewrite moved,
	// commented, to the end of the file.
	DroppedSettings []string `json:"dropped_settings"`
}

func (r settingSetResult) text(w io.Writer, st style.Style) {
	verb := "set"
	if r.Result == resultUnchanged {
		verb = "already set"
	}
	fmt.Fprintf(w, "%s %s to %s\n", st.Success(verb), st.Name(r.Setting.Key), shown(r.Setting.Value))
	switch r.Reload {
	case reloadApplied:
		fmt.Fprintf(w, "  %s\n", st.Muted("the daemon applied it"))
	case resultNotRunning:
		fmt.Fprintf(w, "  %s\n", st.Muted("the daemon is not running; it reads it when it starts"))
	default:
		fmt.Fprintf(w, "  %s\n", st.Warning("saved, but the daemon has not applied it"))
	}
	if len(r.DroppedSettings) > 0 {
		fmt.Fprintf(w, "  %s %s %s\n", st.Muted("dropped:"), strings.Join(r.DroppedSettings, ", "), st.Muted("(commented at the end)"))
	}
}

// RewritesSettings is true when args run a command that writes the config
// file and err names only keys no setting has: such a command moves them out
// of the way, so it runs on the settings that loaded.
func RewritesSettings(args []string, err error) bool {
	e, ok := errors.AsType[*config.Error](err)
	if !ok || !e.OnlyUnknown() {
		return false
	}
	cmd, _, err := newRoot(config.Default()).Find(args)
	if err != nil {
		return false
	}
	path := cmd.CommandPath()
	return path == identity.Name+" setting write" || path == identity.Name+" setting set"
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
	if r.Result == resultUnchanged {
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
		hint = "run " + identity.Name + " setting write, which comments them out at the end of the file"
	}
	p.Hint = &hint
	p.Details = &map[string]any{"settings": e.Problems}
	return p
}
