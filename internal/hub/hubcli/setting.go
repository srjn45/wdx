package hubcli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hub/server"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/settings"
)

// Results of the setting commands, and what the hub did with a setting set.
const (
	resultUpdated    = string(hubapi.ResultUpdated)
	resultUnchanged  = clikit.SettingUnchanged
	resultWritten    = "written"
	reloadApplied    = clikit.ReloadApplied
	reloadNotRunning = clikit.ReloadNotRunning
	reloadNotApplied = clikit.ReloadNotApplied
)

func newSettingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "setting",
		// wdx-hub update runs the release it installs as settings write, as
		// wdx update does, so this name stays for good.
		Aliases: []string{"settings"},
		Short:   "Show and change the settings",
		Long:    name + " help settings explains every setting.",
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
			"List the settings", name+" setting list",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "Each setting's key, value and where the value comes from.\n" +
				`With --json: {"settings": [{"key": ..., "value": ..., "default": ..., "description": ..., "from": ..., "choices": ..., "editable": ...}, ...]}`,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := envOf(cmd).Config
			list := clikit.SettingList{Settings: []clikit.SettingView{}}
			for _, s := range hubconfig.Settings() {
				list.Settings = append(list.Settings, viewOf(cfg, s))
			}
			return clikit.PrintResult(cmd, list)
		},
	}
}

func newSettingGetCmd() *cobra.Command {
	var key string
	cmd := &cobra.Command{
		Use:   "get --name <key>",
		Short: "Show one setting",
		Example: clikit.Examples(
			"Show one setting", name+" setting get --name log.level",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "The setting's value, default, description and where the value comes from.\n" +
				`With --json: {"setting": {"key": ..., "value": ..., "default": ..., "description": ..., "from": ..., "choices": ..., "editable": ...}}`,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := findSetting(key)
			if err != nil {
				return err
			}
			return clikit.PrintResult(cmd, clikit.SettingEnvelope{Setting: viewOf(envOf(cmd).Config, s)})
		},
	}
	cmd.Flags().StringVar(&key, "name", "", "`key` of the setting, such as log.level")
	cmd.MarkFlagRequired("name")
	clikit.CompleteFlag(cmd, "name", clikit.CompleteSettings(hubconfig.Settings(), false))
	return cmd
}

func newSettingSetCmd() *cobra.Command {
	var key, value string
	cmd := &cobra.Command{
		Use:   "set --name <key> --value <value>",
		Short: "Change a setting and have the running hub apply it",
		Long: "The value is checked, saved in the config file, then the running hub reads\n" +
			"the file again: log.level and the otel settings take effect at once, the\n" +
			"listeners, web.tls and public_url when the hub next starts. A variable\n" +
			hubconfig.EnvPrefix + "<KEY> set in the hub's environment wins over the file. The file is\n" +
			"written anew, as by " + name + " setting write, so comments of your own are not kept.",
		Example: clikit.Examples(
			"Log in more detail", name+" setting set --name log.level --value debug",
			"Name the address browsers and nodes reach the hub at", name+" setting set --name public_url --value https://hub.example.com",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not set",
			clikit.OutputNote: "The setting, and what the hub did with it: applied, not_running (it reads the\n" +
				"value when it starts) or not_applied (saved, with a warning on stderr: it takes\n" +
				"effect when the hub next starts, or the hub could not read the file).\n" +
				`With --json: {"result": ..., "setting": {...}, "reload": ..., "dropped_settings": [...]}`,
			clikit.Results: clikit.Annotation(resultUpdated, resultUnchanged),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			log := logging.FromContext(cmd.Context())
			s, err := findSetting(key)
			if err != nil {
				return err
			}
			if s.EnvOnly {
				return clikit.NotInFile(s)
			}
			env := envOf(cmd)
			home := env.Config.Home
			if err := hubconfig.MakeHome(home); err != nil {
				return err
			}
			r, err := hubconfig.Set(home, s.Key, value, homeEnv(env))
			if _, invalid := errors.AsType[*settings.Error](err); invalid {
				return &clikit.Hinted{Err: err, Hint: "see what it takes in " + name + " help settings"}
			}
			if err != nil {
				return err
			}
			result := resultUnchanged
			if r.Changed {
				result = resultUpdated
			}
			log.Info("setting "+result, "name", s.Key, "value", value, "path", hubconfig.ConfigPath(home), "dropped", r.Dropped)
			// What was just written loads: Set checked it and moved away the
			// keys no setting has.
			cfg, err := hubconfig.Load(homeEnv(env))
			if err != nil {
				return err
			}
			if from := cfg.From[s.Key]; from == s.EnvVar {
				st := clikit.StderrStyle(cmd)
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s saved, but %s is set and wins over the file\n", name, st.Warning("warning:"), s.EnvVar)
			}
			return clikit.PrintResult(cmd, clikit.SettingSet{Result: result, Setting: viewOf(cfg, s), Reload: reloadHub(cmd), DroppedSettings: r.Dropped, Server: "the hub"})
		},
	}
	cmd.Flags().StringVar(&key, "name", "", "`key` of the setting, such as log.level")
	cmd.Flags().StringVar(&value, "value", "", "the new `value`")
	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("value")
	clikit.CompleteFlag(cmd, "name", clikit.CompleteSettings(hubconfig.Settings(), true))
	clikit.CompleteFlag(cmd, "value", cobra.NoFileCompletions)
	return cmd
}

// reloadHub has the running hub read the config file again and says what
// came of it. The file is saved by now, so what the hub does not apply only
// gets a warning.
func reloadHub(cmd *cobra.Command) string {
	r, err := clientOf(cmd).reload(cmd.Context())
	st := clikit.StderrStyle(cmd)
	switch {
	case err == nil && len(r.NotApplied) == 0:
		return reloadApplied
	case err == nil:
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s saved; %s take effect when the hub next starts: restart %s\n",
			name, st.Warning("warning:"), strings.Join(r.NotApplied, ", "), st.Command(name+" serve"))
		return reloadNotApplied
	case unavailable(err):
		return reloadNotRunning
	}
	logging.FromContext(cmd.Context()).Warn("hub did not apply the settings", "err", err)
	fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s saved, but the hub did not apply it: %v\n", name, st.Warning("warning:"), err)
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
			"kept.",
		Example: clikit.Examples(
			"Add the settings of this version to the config file", name+" setting write",
			"Write the result to another file, leaving the config file alone", name+" setting write --to /tmp/config.yaml",
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
			path := hubconfig.ConfigPath(home)
			dest, err := clikit.Absolute(cmd, to)
			if err != nil {
				return err
			}
			if dest == "" {
				dest = path
			}
			if dest == path {
				if err := hubconfig.MakeHome(home); err != nil {
					return err
				}
				unlock, err := hubconfig.Lock(home)
				if err != nil {
					return err
				}
				defer unlock()
			}
			r, err := hubconfig.Rewrite(path, nil, homeEnv(envOf(cmd)))
			if err != nil {
				return err
			}
			result := resultUnchanged
			if r.Changed || dest != path {
				if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
					return err
				}
				if err := hubconfig.WriteFile(dest, r.Content); err != nil {
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
	return clikit.FindSetting(name, hubconfig.Settings(), key)
}

// viewOf is s as the commands and the API show it: [secret] for a secret.
func viewOf(cfg hubconfig.Config, s settings.Setting) clikit.SettingView {
	return clikit.NewSettingView(s, cfg.Shown(s.Key), cfg.From[s.Key])
}

// settingStore is the settings in the config file of the home getenv names,
// for the running hub.
type settingStore struct{ getenv func(string) string }

var _ server.SettingStore = settingStore{}

func (s settingStore) List() ([]hubapi.Setting, error) {
	cfg, err := hubconfig.Load(s.getenv)
	if err != nil {
		return nil, err
	}
	list := []hubapi.Setting{}
	for _, setting := range hubconfig.Settings() {
		list = append(list, hubapi.Setting(viewOf(cfg, setting)))
	}
	return list, nil
}

func (s settingStore) Set(key, value string) (hubapi.Setting, bool, error) {
	setting, err := findSetting(key)
	if err != nil {
		return hubapi.Setting{}, false, &server.UnknownSettingError{Key: key}
	}
	if setting.EnvOnly {
		return hubapi.Setting{}, false, &server.InvalidSettingError{Key: key, Err: clikit.NotInFileError(setting)}
	}
	home := s.getenv(hubconfig.EnvPrefix + "HOME")
	r, err := hubconfig.Set(home, key, value, s.getenv)
	if e, invalid := errors.AsType[*settings.Error](err); invalid {
		return hubapi.Setting{}, false, &server.InvalidSettingError{Key: key, Err: clikit.SettingRefusal(e, key)}
	}
	if err != nil {
		return hubapi.Setting{}, false, err
	}
	cfg, err := hubconfig.Load(s.getenv)
	if err != nil {
		return hubapi.Setting{}, false, err
	}
	return hubapi.Setting(viewOf(cfg, setting)), r.Changed, nil
}
