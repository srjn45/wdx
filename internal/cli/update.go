package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
	"github.com/srjn45/wdx/internal/update"
)

const discardChangesQuestion = "discard changes"

func newUpdateCmd(cfg config.Config) *cobra.Command {
	var apply, rollback, discardChanges bool
	var channel, version string
	cmd := &cobra.Command{
		Use:   "update [--apply | --rollback]",
		Short: "Show whether a newer " + identity.Name + " is released, install it or roll it back",
		Long: "Without a flag nothing changes. --apply installs the newest release on the\n" +
			"channel (the update.channel setting, or --channel); the daemon is stopped,\n" +
			"the installed binary, a backup of the database and the config file are\n" +
			"kept, and the daemon starts on the new binary. If any step fails, they are\n" +
			"put back and the daemon starts on the old binary.\n\n" +
			"The release writes the config file anew, as its settings write does: the\n" +
			"values set stay, its new settings are listed, and keys it no longer has are\n" +
			"commented out at the end.\n\n" +
			"--rollback installs the kept version again, with the config file as it was\n" +
			"before the update. The database stays when that version can use it; when a\n" +
			"migration since the update breaks it, the backup is restored, losing every\n" +
			"change since the update, which --discard-changes or the prompt agrees to.\n" +
			"Only one previous version is kept.",
		Example: examples(
			"Show whether a newer release is available", identity.Name+" update",
			"Install it", identity.Name+" update --apply",
			"Install the newest dev release this once", identity.Name+" update --apply --channel dev",
			"Install one release", identity.Name+" update --apply --version 0.2.0",
			"Go back to the version the last update replaced", identity.Name+" update --rollback",
			"From a script, restoring the database backup if the previous version needs it", identity.Name+" update --rollback --discard-changes --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "Without a flag, the installed version, the newest on the channel and whether it is newer;\n" +
				"with --apply or --rollback, the versions and schema versions before and after.\n" +
				`With --json: {"update": {"current": ..., "latest": ..., "channel": ..., "available": ...}}, or` + "\n" +
				`{"result": ..., "update": {"from": ..., "to": ..., "channel": ..., "schema_from": ..., "schema_to": ..., "data_restored": ..., "settings": {"added": [...], "dropped": [...]}}}`,
			resultList: annotation("rolled_back", "up_to_date", "updated"),
			errorList: codeList(api.ErrorCodeChecksumMismatch, api.ErrorCodeDownloadFailed, api.ErrorCodeNoPreviousVersion,
				api.ErrorCodeOlderVersion, api.ErrorCodeRestoreRequired, api.ErrorCodeSettingsInvalid, api.ErrorCodeUpdateFailed, api.ErrorCodeUpdateRunning),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !slices.Contains(config.UpdateChannels, channel) {
				return &usageError{fmt.Errorf("--channel %q is not one of %s", channel, strings.Join(config.UpdateChannels, ", "))}
			}
			if discardChanges && !rollback {
				return &usageError{errors.New("--discard-changes goes with --rollback")}
			}
			o := updateOptions(cmd)
			switch {
			case apply:
				r, err := update.Apply(cmd.Context(), o, channel, version)
				if err != nil {
					return err
				}
				result := "updated"
				if r.UpToDate {
					result = "up_to_date"
				}
				return printResult(cmd, newUpdateResult(result, r, &channel))
			case rollback:
				r, err := rollBack(cmd, o, discardChanges)
				if err != nil {
					return err
				}
				return printResult(cmd, newUpdateResult("rolled_back", r, nil))
			}
			s, err := update.Check(cmd.Context(), o, channel, version)
			if err != nil {
				return err
			}
			return printResult(cmd, updateCheck{Update: updateStatus{Current: s.Current, Latest: s.Latest, Channel: s.Channel, Available: s.Available}})
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "install the newest release on the channel")
	cmd.Flags().BoolVar(&rollback, "rollback", false, "install the version the last update replaced")
	cmd.Flags().StringVar(&channel, "channel", cfg.UpdateChannel, "follow this `channel`: stable or dev; the update.channel setting by default")
	cmd.Flags().StringVar(&version, "version", "", "this release `version` instead of the newest, such as 0.2.0")
	cmd.Flags().BoolVar(&discardChanges, "discard-changes", false, "with --rollback, restore the database backup without asking when the previous version needs it")
	cmd.RegisterFlagCompletionFunc("channel", cobra.FixedCompletions(config.UpdateChannels, cobra.ShellCompDirectiveNoFileComp))
	cmd.RegisterFlagCompletionFunc("version", cobra.NoFileCompletions)
	cmd.MarkFlagsMutuallyExclusive("apply", "rollback")
	cmd.MarkFlagsMutuallyExclusive("rollback", "channel")
	cmd.MarkFlagsMutuallyExclusive("rollback", "version")
	return cmd
}

// updateOptions switches the installed binary and the daemon of the home.
func updateOptions(cmd *cobra.Command) update.Options {
	inv := invocationOf(cmd)
	// Between the old binary and the new daemon the versions differ on
	// purpose: no warning.
	inv.client = client.New(client.Options{Socket: identity.SocketPath(inv.env.Config.Home), DebugID: inv.debugID, Quiet: inv.quiet})
	var progress io.Writer
	if !jsonMode(cmd) {
		progress = cmd.ErrOrStderr()
	}
	return update.Options{
		Home:    inv.env.Config.Home,
		Binary:  inv.env.Executable,
		Current: identity.Version,
		BaseURL: inv.env.Config.UpdateBaseURL,
		Daemon: update.Daemon{
			Stop: func(context.Context) error {
				_, err := stopDaemon(cmd)
				return err
			},
			Start: func(context.Context) (string, error) {
				d, _, err := startDaemon(cmd)
				return d.Version, err
			},
		},
		Progress: progress,
	}
}

// rollBack rolls back, asking before restoring the database backup unless
// discardChanges agreed to it.
func rollBack(cmd *cobra.Command, o update.Options, discardChanges bool) (update.Result, error) {
	r, err := update.Rollback(cmd.Context(), o, discardChanges)
	if discardChanges && r.DataRestored {
		logAnswer(cmd, discardChangesQuestion, "yes", "--discard-changes")
	}
	if _, needed := errors.AsType[*update.RestoreRequiredError](err); !needed {
		return r, err
	}
	if !canPrompt(cmd) {
		logUnanswered(cmd, discardChangesQuestion)
		return r, err
	}
	prompt, st := cmd.ErrOrStderr(), stderrStyle(cmd)
	fmt.Fprintln(prompt, err)
	answer, askErr := ask(prompt, bufio.NewScanner(cmd.InOrStdin()), "restore the backup? "+st.Command("[y/N]")+": ", "y", "n", "")
	if askErr != nil {
		return r, askErr
	}
	if answer != "y" {
		logAnswer(cmd, discardChangesQuestion, "no", "prompt")
		return r, err
	}
	logAnswer(cmd, discardChangesQuestion, "yes", "prompt")
	return update.Rollback(cmd.Context(), o, true)
}

// updateCheck is the installed version and the newest on the channel.
type updateCheck struct {
	Update updateStatus `json:"update"`
}

type updateStatus struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Channel   string `json:"channel"`
	Available bool   `json:"available"`
}

func (c updateCheck) text(w io.Writer, st style.Style) {
	u := c.Update
	if !u.Available {
		fmt.Fprintf(w, "%s: %s %s %s\n", st.Success("up to date"), identity.Name, st.Name(u.Current), st.Muted(fmt.Sprintf("(newest on the %s channel: %s)", u.Channel, u.Latest)))
		return
	}
	fmt.Fprintf(w, "%s: %s %s -> %s %s\n", st.Warning("update available"), identity.Name, u.Current, st.Name(u.Latest), st.Muted("("+u.Channel+" channel)"))
	fmt.Fprintf(w, "  run %s\n", st.Command(identity.Name+" update --apply"))
}

// updateResult is what an update or rollback did.
type updateResult struct {
	Result string       `json:"result"`
	Update updateChange `json:"update"`
}

type updateChange struct {
	From         string  `json:"from"`
	To           string  `json:"to"`
	Channel      *string `json:"channel"` // null for a rollback
	SchemaFrom   int     `json:"schema_from"`
	SchemaTo     int     `json:"schema_to"`
	DataRestored *bool   `json:"data_restored"` // null for an update
	// Settings is how an update wrote the config file anew; null for a
	// rollback or when nothing was newer.
	Settings *updateSettings `json:"settings"`
}

type updateSettings struct {
	Added   []string `json:"added"`
	Dropped []string `json:"dropped"`
}

func newUpdateResult(result string, r update.Result, channel *string) updateResult {
	u := updateChange{From: r.From, To: r.To, Channel: channel, SchemaFrom: r.SchemaFrom, SchemaTo: r.SchemaTo}
	if result == "rolled_back" {
		u.DataRestored = &r.DataRestored
	}
	if r.Settings != nil {
		u.Settings = &updateSettings{Added: r.Settings.Added, Dropped: r.Settings.Dropped}
	}
	return updateResult{Result: result, Update: u}
}

func (r updateResult) text(w io.Writer, st style.Style) {
	u := r.Update
	field := func(name, value string) {
		fmt.Fprintf(w, "  %s %s\n", st.Muted(fmt.Sprintf("%-9s", name)), value)
	}
	schema := fmt.Sprint(u.SchemaTo)
	if u.SchemaFrom != u.SchemaTo {
		schema = fmt.Sprintf("%d -> %d", u.SchemaFrom, u.SchemaTo)
	}
	switch r.Result {
	case "up_to_date":
		fmt.Fprintf(w, "%s: %s %s %s\n", st.Success("up to date"), identity.Name, st.Name(u.To), st.Muted("(newest on the "+*u.Channel+" channel)"))
	case "updated":
		fmt.Fprintf(w, "%s %s %s -> %s\n", st.Success("updated"), identity.Name, u.From, st.Name(u.To))
		field("schema:", schema)
		if s := u.Settings; s != nil && len(s.Added) > 0 {
			field("settings:", "added "+strings.Join(s.Added, ", "))
		}
		if s := u.Settings; s != nil && len(s.Dropped) > 0 {
			field("settings:", "dropped "+strings.Join(s.Dropped, ", ")+" "+st.Muted("(commented at the end of the config file)"))
		}
		field("rollback:", st.Command(identity.Name+" update --rollback")+" goes back to "+u.From)
	default:
		fmt.Fprintf(w, "%s %s %s -> %s\n", st.Success("rolled back"), identity.Name, u.From, st.Name(u.To))
		field("schema:", schema)
		if *u.DataRestored {
			field("database:", st.Warning("restored from the backup taken at the update"))
		} else {
			field("database:", "kept")
		}
	}
}

// updateProblem returns the problem of an update that failed, ok false when
// err is no update error.
func updateProblem(err error) (p api.Problem, ok bool) {
	hint := func(h string) { p.Hint = &h }
	if e, found := errors.AsType[*update.DownloadError](err); found {
		p = api.NewProblem(api.ErrorCodeDownloadFailed, err.Error())
		p.Details = &map[string]any{"url": e.URL}
		hint("check the version, the network and the update.base_url setting")
	} else if e, found := errors.AsType[*update.ChecksumError](err); found {
		p = api.NewProblem(api.ErrorCodeChecksumMismatch, err.Error())
		p.Details = &map[string]any{"archive": e.Archive}
		hint("try again; nothing was installed")
	} else if _, found := errors.AsType[*update.OlderError](err); found {
		p = api.NewProblem(api.ErrorCodeOlderVersion, err.Error())
		hint(identity.Name + " update --rollback goes back to the version the last update replaced")
	} else if _, found := errors.AsType[*update.RunningError](err); found {
		p = api.NewProblem(api.ErrorCodeUpdateRunning, err.Error())
		hint("wait until it ends")
	} else if e, found := errors.AsType[*update.FailedError](err); found {
		p = api.NewProblem(api.ErrorCodeUpdateFailed, err.Error())
		p.Details = &map[string]any{"step": e.Step, "undone": e.UndoErr == nil, "version": e.Version}
		hint(e.Version + " is installed and running again; the debug_id finds what failed in the logs")
		if e.UndoErr != nil {
			hint("reinstall with scripts/install.sh; the debug_id finds what failed in the logs")
		}
	} else if e, found := errors.AsType[*update.SettingsError](err); found {
		p = api.NewProblem(api.ErrorCodeSettingsInvalid, err.Error())
		p.Details = &map[string]any{"version": e.Version, "settings": e.Details}
		hint("change them in the config file, then update again; nothing was installed")
	} else if _, found := errors.AsType[*update.NoPreviousError](err); found {
		p = api.NewProblem(api.ErrorCodeNoPreviousVersion, err.Error())
		hint("only the version " + identity.Name + " update --apply replaced is kept, until a rollback")
	} else if e, found := errors.AsType[*update.RestoreRequiredError](err); found {
		p = api.NewProblem(api.ErrorCodeRestoreRequired, err.Error())
		p.Details = &map[string]any{"to": e.To, "schema": e.Schema, "oldest": e.Oldest, "known": e.Known}
		hint("pass --discard-changes to restore the backup")
	} else {
		return api.Problem{}, false
	}
	return p, true
}
