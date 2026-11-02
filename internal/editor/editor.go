// Package editor knows the terminal editors wdx runs on a file: how to start
// each, and what to type into it to save or discard the file and quit
// (docs/TIDE_CODE.md). It is data with no dependencies, so that the settings,
// wdx deps, the daemon and tide share it.
package editor

import "strings"

// Default is the editor the editor setting names when it is not set.
const Default = "vi"

// Editor is a terminal editor wdx can run on a file.
type Editor struct {
	// Name is how the editor setting and wdx deps name it, and the program run.
	Name string
	args func(path string) []string // what comes before the file
	// save and discard are what to write to it, in whatever mode it is, to save or
	// leave unchanged the file path it was started on, and quit. Another file edited
	// in it is left alone, and keeps it running.
	save, discard func(path string) string
}

// Command is what runs the editor on path.
func (e Editor) Command(path string) []string {
	cmd := append([]string{e.Name}, e.args(path)...)
	return append(cmd, "--", path)
}

// Save is what to type into the editor started on path to save that file and
// quit.
func (e Editor) Save(path string) string { return e.save(path) }

// Discard is what to type into the editor started on path to quit leaving that
// file as it is on disk.
func (e Editor) Discard(path string) string { return e.discard(path) }

// keys is a save or discard that is the same for every file.
func keys(s string) func(string) string { return func(string) string { return s } }

// args is a command's arguments that are the same for every file.
func args(a ...string) func(string) []string { return func(string) []string { return a } }

// vimSave and vimDiscard are vi's, vim's and Neovim's. C-\ C-n leaves any mode
// and drops a command half typed, which Esc would run in vi's compatible mode.
// Buffer 1 is the file it started on, which spares quoting its name.
var (
	vimSave    = keys("\x1c\x0e:b1 | x\r")
	vimDiscard = keys("\x1c\x0e:b1 | q!\r")
)

var editors = []Editor{
	{
		Name: "vi",
		// -n keeps no swap file, which would sit beside the file in the worktree, where
		// git and agents see it. Ubuntu's vi turns esckeys off after reading its
		// arguments' --cmd, so an arrow key in insert mode would type letters; -c comes
		// later.
		args: args("-n", "-c", "set esckeys"),
		save: vimSave, discard: vimDiscard,
	},
	{
		Name: "vim",
		// As vi: a vimrc may set compatible, which turns esckeys off.
		args: args("-n", "-c", "set esckeys"),
		save: vimSave, discard: vimDiscard,
	},
	{
		Name: "nvim",
		// Neovim has no esckeys: it always reads arrows as keys.
		args: args("-n"),
		save: vimSave, discard: vimDiscard,
	},
	{
		Name: "nano",
		// C-c leaves a prompt, then C-x asks whether to save a file edited; nano quits
		// at once when it was not, and what follows reaches no one.
		args:    args(),
		save:    keys("\x03\x18y\r"),
		discard: keys("\x03\x18n"),
	},
	{
		Name: "hx",
		args: args(),
		// :open brings the file to the front, wherever the person was; :update writes
		// it only when edited, as :write would refuse a file another program wrote
		// even then. :quit-all waits for that write, which :quit does not before it
		// looks for files edited, and refuses while another file is edited, or this
		// one after :update failed.
		save: func(path string) string {
			return helixEscape + ":open " + helixQuote(path) + "\r:update\r:quit-all\r"
		},
		// :buffer-close! drops the file's edits; hx shows an empty buffer in its
		// place.
		discard: func(path string) string {
			return helixEscape + ":open " + helixQuote(path) + "\r:buffer-close!\r:quit-all\r"
		},
	},
	{
		Name: "emacs",
		// -nw keeps a GUI build in the terminal. No backup, auto-save or lock file is
		// made, which would sit beside the file in the worktree, as vi's -n. Save and
		// discard are keys of their own, which any state reads: C-g, or leaving a
		// prompt, would drop the keys typed after it. Each saves, or forgets the
		// edits of, the buffer of the file it started on, whichever is current, then
		// quits as C-x C-c would, asking about any other buffer edited; from a
		// prompt, once it has left it.
		args: func(path string) []string {
			return []string{"-nw", "--eval", `(progn
(setq make-backup-files nil auto-save-default nil create-lockfiles nil)
(defvar wdx-file (expand-file-name ` + elispQuote(path) + `))
(defun wdx-quit (save)
  (let ((buffer (find-buffer-visiting wdx-file)))
    (when buffer
      (with-current-buffer buffer
        (if save (save-buffer) (set-buffer-modified-p nil)))))
  (save-buffers-kill-terminal))
(defun wdx-quit-later (save)
  (if (not (minibufferp))
      (wdx-quit save)
    (run-at-time 0 nil #'wdx-quit save)
    (abort-recursive-edit)))
(define-key input-decode-map "` + emacsSave + `" [wdx-save])
(define-key input-decode-map "` + emacsDiscard + `" [wdx-discard])
(global-set-key [wdx-save] (lambda () (interactive) (wdx-quit-later t)))
(global-set-key [wdx-discard] (lambda () (interactive) (wdx-quit-later nil))))`}
		},
		save:    keys(emacsSave),
		discard: keys(emacsDiscard),
	},
}

// emacsSave and emacsDiscard are keys no terminal sends, which emacs is told at
// its start to read as wdx's.
const (
	emacsSave    = "\x1b[7001~"
	emacsDiscard = "\x1b[7002~"
)

// helixEscape is Esc as hx reads it however soon a key follows: Esc then :
// in one read is Alt+:. It leaves insert or select mode, or a prompt.
const helixEscape = "\x1b[27u"

// helixQuote quotes path as an argument of a Helix command: single quotes take
// it as it is, % included; one holding a quote goes in double quotes, where
// %% is a %.
func helixQuote(path string) string {
	if !strings.Contains(path, "'") {
		return "'" + path + "'"
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(path) + `"`
}

// elispQuote quotes s as an Emacs Lisp string.
func elispQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
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
