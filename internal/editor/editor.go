// Package editor knows the terminal editors wdx runs on a file: how to start
// each, and what to type into it to save or discard the file and quit
// (docs/TIDE_CODE.md). It is data with no dependencies, so that the settings,
// wdx deps, the daemon and tide share it.
package editor

// Default is the editor the editor setting names when it is not set.
const Default = "vi"

// Editor is a terminal editor wdx can run on a file.
type Editor struct {
	// Name is how the editor setting and wdx deps name it, and the program run.
	Name string
	args []string // what comes before the file
	// Save and Discard are what to write to it, in whatever mode it is, to save or
	// leave unchanged the file it was started on, and quit. Another file edited in it
	// is left alone, and keeps it running.
	Save, Discard string
}

// Command is what runs the editor on path.
func (e Editor) Command(path string) []string {
	cmd := append([]string{e.Name}, e.args...)
	return append(cmd, "--", path)
}

var editors = []Editor{
	{
		Name: "vi",
		// Ubuntu's vi turns esckeys off after reading its arguments' --cmd, so an
		// arrow key in insert mode would type letters; -c comes later.
		args: []string{"-c", "set esckeys"},
		// C-\ C-n leaves any mode and drops a command half typed, which Esc would run
		// in vi's compatible mode. Buffer 1 is the file vi started on, which spares
		// quoting its name.
		Save:    "\x1c\x0e:b1 | x\r",
		Discard: "\x1c\x0e:b1 | q!\r",
	},
}

// Names are the editors supported, in the order they are listed.
func Names() []string {
	names := make([]string, len(editors))
	for i, e := range editors {
		names[i] = e.Name
	}
	return names
}

// Lookup returns the editor named name.
func Lookup(name string) (Editor, bool) {
	for _, e := range editors {
		if e.Name == name {
			return e, true
		}
	}
	return Editor{}, false
}
