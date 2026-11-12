package aca

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/logging"
)

// What Cursor's status line is and keeps, in the backup (the wdx doc CURSOR_CONTEXT).
const (
	// cursorStatusFile is what Cursor last gave its status line.
	cursorStatusFile = "cursor-status.json"
	// cursorStatusScript is the status line's command, which Cursor runs as `sh
	// <script>`: it splits its statusLine command on whitespace and quotes as a shell
	// would, but runs no shell (string-argv, spawned without one; checked on
	// 2026.11.05), so wdx's cat and mv, and the user's command in braces, need one.
	cursorStatusScript = "cursor-status.sh"
)

// cursorStatusLine writes cursorStatusScript in the backup, statusLineCommand's text
// with Cursor's footer and the user's own command after wdx's, and returns the statusLine for the agent's
// settings: `sh <script>`, keeping the user's padding, updateIntervalMs and timeoutMs.
// users is the user's own statusLine; one that is not a command gives wdx's alone.
func cursorStatusLine(ctx context.Context, l Launch, users json.RawMessage) (json.RawMessage, error) {
	var theirs struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	var kept map[string]json.RawMessage
	if len(users) > 0 {
		if err := json.Unmarshal(users, &theirs); err != nil {
			logging.FromContext(ctx).Warn("user's cursor status line not read", "err", err)
		} else if err := json.Unmarshal(users, &kept); err != nil {
			return nil, err
		}
	}
	var command string
	if theirs.Type == "command" {
		command = strings.TrimSpace(theirs.Command)
	}
	script := filepath.Join(l.BackupDir, cursorStatusScript)
	arg, err := cursorArgument(script)
	if err != nil {
		return nil, err
	}
	text := statusLineCommand(filepath.Join(l.BackupDir, cursorStatusFile), cursorFooter(l.UserHome), command) + "\n"
	if err := os.MkdirAll(l.BackupDir, 0o700); err != nil {
		return nil, err
	}
	if err := atomicfile.Write(script, []byte(text), 0o600); err != nil {
		return nil, err
	}
	line := map[string]any{"type": "command", "command": "sh " + arg}
	if command != "" {
		for _, key := range []string{"padding", "updateIntervalMs", "timeoutMs"} {
			if value, ok := kept[key]; ok {
				line[key] = value
			}
		}
	}
	return json.Marshal(line)
}

// cursorFooter is the script's part that prints what Cursor draws under its box when
// it has no status line, which it no longer draws under one (checked on 2026.11.05,
// the wdx doc CURSOR_FOOTER), from the JSON just saved in "$f":
//
//	Grok 4.5 High · 4.7%
//	~/w/repo · main
//
// The model's display_name, then the share of the context used rounded to a tenth,
// as Cursor shows it, or the model alone while the share is null; then cwd, home
// shortened to ~, with the branch HEAD names when the status line runs in a
// repository's root or a worktree's, whose .git is a file naming its gitdir. Cursor
// runs it in the agent's directory, which is cwd. The shell reads the branch, and one
// sed run the rest, so that no jq nor git is needed: a value sed cannot read, a
// display_name or cwd holding a quote or a backslash, or a share that is not a plain
// number, leaves its part out, and a line left with nothing is not printed. The
// branch comes to sed as the last line of its input, after the JSON, which may not
// end its own: sed would end its output as its input ends.
func cursorFooter(home string) string {
	home = filepath.Clean(home)
	shorten := ""
	if filepath.IsAbs(home) && home != "/" && !strings.ContainsAny(home, "\"\\\n") {
		shorten = fmt.Sprintf("s/^%[1]s\\//~\\//\ns/^%[1]s\\n/~\\\n/\n", sedLiteral(home))
	}
	return cursorBranch + "printf '%s\\n' \"$b\" | LC_ALL=C sed -n '" +
		strings.ReplaceAll(fmt.Sprintf(cursorFooterSed, shorten), "'", `'\''`) + `' "$f" -`
}

// cursorBranch sets b to the branch .git/HEAD names, or "" for none or a detached
// HEAD; read and its redirection fail quietly on a .git the shell cannot read.
const cursorBranch = `b=
if [ -f .git ]; then { read -r b < .git && read -r b < "${b#gitdir: }/HEAD"; } 2>/dev/null
elif [ -d .git ]; then { read -r b < .git/HEAD; } 2>/dev/null
fi
case $b in "ref: refs/heads/"?*) b=${b#ref: refs/heads/} ;; *) b= ;; esac
`

// cursorFooterSed is the sed program of cursorFooter, %s the lines that shorten the
// home. The JSON's lines are joined in the hold space, then its last line, the
// branch, put after them, so that the hold is "JSON\nbranch" with no other newline;
// the directory's line turns it around, so that a branch never matches its "cwd". A t right
// after a label only clears t's flag, so that the next t sees one substitution.
//
// The share is rounded on its digits: "4.75" is written "4.750", a 0 making sure of a
// tenth; past the tenth, a digit of 5 or more marks "47+" to carry, which turns the
// nines before the mark to zeros and adds one to the digit before them, as "99+" is
// "100"; then the point goes back before the last digit.
const cursorFooterSed = `$!{
H
d
}
x
s/\n/ /g
G
h
s/\n.*//
t model
:model
s/^\(.*"display_name"[[:space:]]*:[[:space:]]*"\([^"\\]*\)".*\)$/\2\
\1/
t share
s/^/\
/
:share
t number
:number
s/\n.*"used_percentage"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\)\.\{0,1\}\([0-9]*\)[[:space:]]*[,}].*$/\
\1.\20/
t round
s/\n.*$//
b first
:round
s/\.\([0-9]\)[5-9][0-9]*$/\1+/
t carry
s/\.\([0-9]\)[0-9]*$/\1/
b point
:carry
s/9+\(0*\)$/+0\1/
t carry
s/\n+\(0*\)$/\
1\1/
s/8+\(0*\)$/9\1/
s/7+\(0*\)$/8\1/
s/6+\(0*\)$/7\1/
s/5+\(0*\)$/6\1/
s/4+\(0*\)$/5\1/
s/3+\(0*\)$/4\1/
s/2+\(0*\)$/3\1/
s/1+\(0*\)$/2\1/
s/0+\(0*\)$/1\1/
:point
s/\([0-9]\)$/.\1%%/
s/^\n//
s/\n/ · /
:first
/./p
g
s/^\(.*\)\n\(.*\)$/\2\
\1/
t cwd
:cwd
s/^\(.*\)\n.*"cwd"[[:space:]]*:[[:space:]]*"\([^"\\]*\)".*$/\2\
\1/
t directory
b second
:directory
%ss/\n\(..*\)$/ · \1/
s/\n$//
/./p
:second
`

// sedLiteral is s as a basic regular expression matching it alone, between slashes:
// each character special there in brackets of its own, a slash escaped. s holds no
// backslash or newline, and starts with a slash, so a caret in it is plain.
func sedLiteral(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '.', '*', '[', '$':
			b.WriteString("[" + string(r) + "]")
		case '/':
			b.WriteString(`\/`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// cursorArgument is path as one argument of a command Cursor splits: on whitespace,
// with single or double quotes around an argument and no escapes (string-argv). It is
// given as it is when it holds none of those, else in the kind of quote it does not
// hold; one holding both kinds cannot be given.
func cursorArgument(path string) (string, error) {
	switch {
	case !strings.ContainsAny(path, `"'`) && strings.IndexFunc(path, unicode.IsSpace) < 0:
		return path, nil
	case !strings.Contains(path, `"`):
		return `"` + path + `"`, nil
	case !strings.Contains(path, `'`):
		return `'` + path + `'`, nil
	}
	return "", fmt.Errorf("Cursor cannot be given the path %q: it holds both kinds of quote", path)
}

// ContextSize implements AiCodingAgent, from what Cursor last gave its status line: the
// tokens in the context, the share of the window it shows rounded to tokens, and the
// model's window. Before the first message and after /clear Cursor gives null, read as
// 0 tokens and no window; in a chat whose size it knows, the size stays through a turn
// and changes with each response, so after a compaction it is the summary's request's
// only at the next response. The status file is in the backup, so there is nothing
// else to follow. Checked on 2026.11.05 (the wdx doc CURSOR_CONTEXT).
func (Cursor) ContextSize(l Launch) (ContextSize, []string, error) {
	if l.BackupDir == "" {
		return ContextSize{}, nil, ErrNoContextSize
	}
	size, err := readStatusLine(l.BackupDir, cursorStatusFile)
	// Its screen gives the model on every read.
	size.Model, size.ModelAt = "", time.Time{}
	return size, nil, err
}
