package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/daemon"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/settings"
)

func newSettingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "setting",
		// Every installed wdx runs the release it updates to as settings
		// write, so this name stays for good: docs/SETTINGS.md.
		Aliases: []string{"settings"},
		Short:   "Show and change the settings",
		Long:    identity.Name + " help settings explains every setting.",
		Args:    clikit.NoArgs,
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newSettingListCmd(), newSettingGetCmd(), newSettingSetCmd(), newSettingWriteCmd())
	return cmd
}

func newSettingListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every setting with its value",
		Example: clikit.Examples(
			"List the settings", identity.Name+" setting list",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "Each setting's key, value and where the value comes from.\n" +
				`With --json: {"settings": [{"key": ..., "value": ..., "default": ..., "description": ..., "from": ..., "choices": ..., "editable": ...}, ...]}`,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := envOf(cmd).Config
			list := clikit.SettingList{Settings: []clikit.SettingView{}}
			for _, s := range config.Settings() {
				list.Settings = append(list.Settings, viewOf(cfg, s))
			}
			return clikit.PrintResult(cmd, list)
		},
	}
}

func newSettingGetCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "get --name <key>",
		Short: "Show one setting",
		Example: clikit.Examples(
			"Show one setting", identity.Name+" setting get --name log.level",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "The setting's value, default, description and where the value comes from.\n" +
				`With --json: {"setting": {"key": ..., "value": ..., "default": ..., "description": ..., "from": ..., "choices": ..., "editable": ...}}`,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := findSetting(name)
			if err != nil {
				return err
			}
			return clikit.PrintResult(cmd, clikit.SettingEnvelope{Setting: viewOf(envOf(cmd).Config, s)})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "`key` of the setting, such as log.level")
	cmd.MarkFlagRequired("name")
	clikit.CompleteFlag(cmd, "name", clikit.CompleteSettings(config.Settings(), false))
	return cmd
}

func newSettingSetCmd() *cobra.Command {
	var name, value string
	cmd := &cobra.Command{
		Use:   "set --name <key> --value <value>",
		Short: "Change a setting and apply it at once",
		Long: "The value is checked, saved in the config file, then applied: the daemon\n" +
			"reads the file again, and every later command reads it anyway. Terminals\n" +
			"already running keep their shell and history limit, and a running TIDE its\n" +
			"colors. The file is written anew, as by " + identity.Name + " setting write, so comments\n" +
			"of your own are not kept.",
		Example: clikit.Examples(
			"Log in more detail", identity.Name+" setting set --name log.level --value debug",
			"Follow the dev releases", identity.Name+" setting set --name update.channel --value dev",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not set",
			clikit.OutputNote: "The setting, and what the daemon did with it: applied, not_running (it reads the\n" +
				"value when it starts) or not_applied (saved, with a warning on stderr to restart it).\n" +
				`With --json: {"result": ..., "setting": {...}, "reload": ..., "dropped_settings": [...]}`,
			clikit.Results: clikit.Annotation(resultUpdated, resultUnchanged),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			log := logging.FromContext(cmd.Context())
			s, err := findSetting(name)
			if err != nil {
				return err
			}
			if s.EnvOnly {
				return clikit.NotInFile(s)
			}
			home := envOf(cmd).Config.Home
			r, err := config.Set(home, s.Key, value)
			if _, invalid := errors.AsType[*settings.Error](err); invalid {
				return &clikit.Hinted{Err: err, Hint: "see what it takes in " + identity.Name + " help settings"}
			}
			if err != nil {
				return err
			}
			result := resultUnchanged
			if r.Changed {
				result = resultUpdated
			}
			// What was just written loads: Rewrite checked it and moved away
			// the keys no setting has.
			cfg, err := config.LoadHome(home)
			if err != nil {
				return err
			}
			log.Info("setting "+result, "name", s.Key, "value", cfg.Shown(s.Key), "path", identity.ConfigPath(home), "dropped", r.Dropped)
			return clikit.PrintResult(cmd, clikit.SettingSet{Result: result, Setting: viewOf(cfg, s), Reload: reloadDaemon(cmd), DroppedSettings: r.Dropped, Server: "the daemon"})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "`key` of the setting, such as log.level")
	cmd.Flags().StringVar(&value, "value", "", "the new `value`")
	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("value")
	clikit.MeasureFlag(cmd, "value")
	clikit.CompleteFlag(cmd, "name", clikit.CompleteSettings(config.Settings(), true))
	clikit.CompleteFlag(cmd, "value", cobra.NoFileCompletions)
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
	st := clikit.StderrStyle(cmd)
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
		Example: clikit.Examples(
			"Add the settings of this version to the config file", identity.Name+" setting write",
			"Write the result to another file, leaving the config file alone", identity.Name+" setting write --to /tmp/config.yaml",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not written",
			clikit.OutputNote: "The file written, the settings it lists that the file did not and the keys moved to its end.\n" +
				`With --json: {"result": ..., "settings": {"path": ..., "added": [...], "dropped": [...]}}`,
			clikit.Results: clikit.Annotation(resultUnchanged, resultWritten),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			log := logging.FromContext(cmd.Context())
			home := envOf(cmd).Config.Home
			path := identity.ConfigPath(home)
			dest, err := clikit.Absolute(cmd, to)
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
			return clikit.PrintResult(cmd, clikit.SettingsWritten{Result: result, Settings: clikit.SettingsChange{Path: dest, Added: r.Added, Dropped: r.Dropped}})
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "write it to `path` instead of the config file")
	clikit.CompleteFlag(cmd, "to", clikit.CompleteFiles)
	return cmd
}

// findSetting returns the setting named key.
func findSetting(key string) (settings.Setting, error) {
	return clikit.FindSetting(identity.Name, config.Settings(), key)
}

// viewOf is s as the commands and the API show it.
func viewOf(cfg config.Config, s settings.Setting) clikit.SettingView {
	return clikit.NewSettingView(s, cfg.Value(s.Key), cfg.From[s.Key])
}

// settingStore is the settings in the config file of a home, for the daemon.
type settingStore struct{ home string }

func (s settingStore) List() ([]api.Setting, error) {
	cfg, err := config.LoadHome(s.home)
	if err != nil {
		return nil, err
	}
	list := []api.Setting{}
	for _, setting := range config.Settings() {
		list = append(list, api.Setting(viewOf(cfg, setting)))
	}
	return list, nil
}

func (s settingStore) Set(key, value string) (api.Setting, bool, error) {
	list := config.Settings()
	i := slices.IndexFunc(list, func(setting settings.Setting) bool { return setting.Key == key })
	if i < 0 {
		return api.Setting{}, false, &daemon.UnknownSettingError{Key: key}
	}
	if list[i].EnvOnly {
		return api.Setting{}, false, &daemon.InvalidSettingError{Key: key, Err: clikit.NotInFileError(list[i])}
	}
	r, err := config.Set(s.home, key, value)
	if e, invalid := errors.AsType[*settings.Error](err); invalid {
		return api.Setting{}, false, &daemon.InvalidSettingError{Key: key, Err: clikit.SettingRefusal(e, key)}
	}
	if err != nil {
		return api.Setting{}, false, err
	}
	cfg, err := config.LoadHome(s.home)
	if err != nil {
		return api.Setting{}, false, err
	}
	return api.Setting(viewOf(cfg, list[i])), r.Changed, nil
}
