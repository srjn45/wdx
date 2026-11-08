package clikit

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/srjn45/wdx/internal/settings"
	"github.com/srjn45/wdx/internal/style"
)

// What the server a program runs did with a setting set: SettingSet's Reload.
const (
	ReloadApplied    = "applied"
	ReloadNotRunning = "not_running"
	ReloadNotApplied = "not_applied"
)

// SettingView is a setting as a program's setting commands and API show it.
// Its fields are a generated API's Setting, name for name and in order, so
// that the one converts to the other.
type SettingView struct {
	Choices     *[]string `json:"choices"` // null when it takes any value
	Default     string    `json:"default"`
	Description string    `json:"description"`
	Editable    bool      `json:"editable"` // false when the config file cannot set it
	From        string    `json:"from"`
	Key         string    `json:"key"`
	Value       string    `json:"value"`
}

// NewSettingView is s with value, as it may be shown, taken from from.
func NewSettingView(s settings.Setting, value, from string) SettingView {
	v := SettingView{Key: s.Key, Value: value, Default: s.Default, Description: s.Description, From: from, Editable: !s.EnvOnly}
	if s.Choices != nil {
		v.Choices = &s.Choices
	}
	return v
}

// FindSetting returns the setting of list named key, or a usage error that
// hints at program's setting list.
func FindSetting(program string, list []settings.Setting, key string) (settings.Setting, error) {
	if i := slices.IndexFunc(list, func(s settings.Setting) bool { return s.Key == key }); i >= 0 {
		return list[i], nil
	}
	return settings.Setting{}, &Hinted{Err: &UsageError{Err: fmt.Errorf("no setting is named %q", key)}, Hint: "see every setting with " + program + " setting list"}
}

// NotInFile is the usage error of setting s in the config file, which s is
// not read from.
func NotInFile(s settings.Setting) error {
	return &Hinted{Err: &UsageError{Err: NotInFileError(s)}, Hint: "set " + s.EnvVar + " in the environment"}
}

// NotInFileError says s is read from the environment only.
func NotInFileError(s settings.Setting) error {
	return fmt.Errorf("%s is read from %s, not the config file", s.Key, s.EnvVar)
}

// SettingRefusal is why the setting key was refused: only that, when its own
// value is what was wrong, for a row that already names it.
func SettingRefusal(e *settings.Error, key string) error {
	for _, p := range e.Problems {
		if p.Key == key && p.Value != nil {
			return errors.New(p.Reason)
		}
	}
	return e
}

// shownValue is a value as text shows it, where an empty one would not show.
func shownValue(value string) string {
	return cmp.Or(value, `""`)
}

// SettingList is what setting list prints.
type SettingList struct {
	Settings []SettingView `json:"settings"`
}

func (l SettingList) Text(w io.Writer, st style.Style) {
	var table style.Table
	for _, s := range l.Settings {
		table.Row(st.Name(s.Key), shownValue(s.Value), st.Muted(s.From))
	}
	table.Write(w)
}

// SettingEnvelope is what setting get prints.
type SettingEnvelope struct {
	Setting SettingView `json:"setting"`
}

func (e SettingEnvelope) Text(w io.Writer, st style.Style) {
	s := e.Setting
	fmt.Fprintf(w, "%s %s\n", st.Name(s.Key), shownValue(s.Value))
	field := FieldPrinter(w, st, len("description"))
	field("description", s.Description)
	field("default", shownValue(s.Default))
	field("from", s.From)
}

// SettingSet is what setting set did.
type SettingSet struct {
	Result  string      `json:"result"`
	Setting SettingView `json:"setting"`
	// Reload is what the server did: ReloadApplied, ReloadNotRunning or
	// ReloadNotApplied.
	Reload string `json:"reload"`
	// DroppedSettings are the keys no setting has that the rewrite moved,
	// commented, to the end of the file.
	DroppedSettings []string `json:"dropped_settings"`
	// Server names the server text says applied it: the daemon, the hub.
	Server string `json:"-"`
}

// SettingUnchanged is the result of setting a setting to the value it has.
const SettingUnchanged = "unchanged"

func (r SettingSet) Text(w io.Writer, st style.Style) {
	verb := "set"
	if r.Result == SettingUnchanged {
		verb = "already set"
	}
	fmt.Fprintf(w, "%s %s to %s\n", st.Success(verb), st.Name(r.Setting.Key), shownValue(r.Setting.Value))
	switch r.Reload {
	case ReloadApplied:
		fmt.Fprintf(w, "  %s\n", st.Muted(r.Server+" applied it"))
	case ReloadNotRunning:
		fmt.Fprintf(w, "  %s\n", st.Muted(r.Server+" is not running; it reads it when it starts"))
	default:
		fmt.Fprintf(w, "  %s\n", st.Warning("saved, but "+r.Server+" has not applied it"))
	}
	if len(r.DroppedSettings) > 0 {
		fmt.Fprintf(w, "  %s %s %s\n", st.Muted("dropped:"), strings.Join(r.DroppedSettings, ", "), st.Muted("(commented at the end)"))
	}
}

// SettingsWritten is what setting write did.
type SettingsWritten struct {
	Result   string         `json:"result"`
	Settings SettingsChange `json:"settings"`
}

// SettingsChange is the file setting write wrote and what changed in it.
type SettingsChange struct {
	Path    string   `json:"path"`
	Added   []string `json:"added"`
	Dropped []string `json:"dropped"`
}

func (r SettingsWritten) Text(w io.Writer, st style.Style) {
	s := r.Settings
	if r.Result == SettingUnchanged {
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
