package hubconfig

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

// Layout is where an installed hub's files are, as install-hub.sh installs
// them and wdx-hub update updates them.
type Layout struct {
	User   string // the system user the service runs as, whose home holds the hub's
	Binary string // the binary the service runs
	Unit   string // the systemd unit
	Lib    string // root's: the update's lock and downloads, and the previous version
	// Completions are where each shell's completion is installed for every
	// user, for the shells this machine has.
	Completions []Completion
}

// Completion is the file a shell loads the completion of wdx-hub from.
type Completion struct {
	Shell string // bash, zsh or fish, as wdx-hub completion names it
	Path  string
}

// Installed is the layout of an installed hub, the one wdx-hub update
// updates. Tests give one of their own.
var Installed = Layout{
	User:   Name,
	Binary: "/usr/local/bin/" + Name,
	Unit:   "/etc/systemd/system/" + Service,
	Lib:    "/usr/local/lib/" + Name,
	Completions: []Completion{
		{"bash", "/usr/share/bash-completion/completions/" + Name},
		{"zsh", "/usr/local/share/zsh/site-functions/_" + Name},
		{"fish", "/usr/share/fish/vendor_completions.d/" + Name + ".fish"},
	},
}

// Where the hub's releases are published: with wdx's, on GitHub.
const (
	// ReleasesURL holds the releases, as <ReleasesURL>/v<version>/<archive>.
	ReleasesURL = "https://github.com/srjn45/wdx/releases/download"
	// ReleasesAPI answers which stable release is the latest.
	ReleasesAPI = "https://api.github.com/repos/srjn45/wdx/releases"
	// DevLatestURL names the newest release of either kind.
	DevLatestURL = "https://raw.githubusercontent.com/srjn45/wdx/channels/LATEST-dev"
)

// Service is the systemd unit's name.
const Service = Name + ".service"

// Previous is the directory the version an update replaced is kept in.
func (l Layout) Previous() string { return filepath.Join(l.Lib, "previous") }

// Present reports whether this machine has the hub installed in l: its
// binary and its unit.
func (l Layout) Present() bool {
	for _, path := range []string{l.Binary, l.Unit} {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}

// Account is a user of the system, as its passwd and group entries give it.
type Account struct {
	Name     string
	UID, GID int
	Groups   []int
	Home     string // its home directory, which the hub's home is under by default
}

// LookupAccount returns the system user name.
func LookupAccount(name string) (Account, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return Account{}, err
	}
	a := Account{Name: u.Username, Home: u.HomeDir}
	if a.UID, err = strconv.Atoi(u.Uid); err != nil {
		return Account{}, fmt.Errorf("user %s: uid %q: %w", name, u.Uid, err)
	}
	if a.GID, err = strconv.Atoi(u.Gid); err != nil {
		return Account{}, fmt.Errorf("user %s: gid %q: %w", name, u.Gid, err)
	}
	groups, err := u.GroupIds()
	if err != nil {
		return Account{}, fmt.Errorf("user %s: groups: %w", name, err)
	}
	for _, g := range groups {
		id, err := strconv.Atoi(g)
		if err != nil {
			return Account{}, fmt.Errorf("user %s: group %q: %w", name, g, err)
		}
		a.Groups = append(a.Groups, id)
	}
	return a, nil
}
