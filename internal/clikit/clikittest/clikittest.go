// Package clikittest checks a program's commands as the cli's tests do: that
// every run is logged from the command line received to the response
// returned, that --json prints one object, that each command returns only
// what its help lists, and that every command's help is complete.
package clikittest

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/pty"
	"github.com/srjn45/wdx/internal/waittest"
)

// Output is what a run printed and returned.
type Output struct {
	Exit           int
	Stdout, Stderr string
	Pid            int // the process's that ran it; 0 for this one
}

// Logged is where a test home's runs are logged, and the messages that
// count the requests a run made to a server: one it had answered, one it gave
// up on, and the server's end of one, logged under the run's debug id.
type Logged struct {
	Dir                       string
	Called, Abandoned, Served string
}

// LogLine is a line of a log, as far as the checks read it.
type LogLine struct {
	Time     string          `json:"time"`
	Level    string          `json:"level"`
	Msg      string          `json:"msg"`
	DebugID  string          `json:"debug_id"`
	Process  string          `json:"process"`
	Pid      int             `json:"pid"`
	Args     json.RawMessage `json:"args"` // a command's arguments, or SQL's
	Path     string          `json:"path"`
	Exit     *int            `json:"exit"`
	Code     string          `json:"code"`
	Response json.RawMessage `json:"response"`
}

// logsRead holds, by directory, the log lines parsed so far: every run in a
// test reads its home's logs, which parsed whole each time cost more the
// longer the test.
var logsRead sync.Map

// logFile is what has been parsed of one log file.
type logFile struct {
	read  int64
	lines []LogLine
}

// dirLogs is what has been parsed of a directory's log files, by path.
type dirLogs struct {
	mu    sync.Mutex
	files map[string]*logFile
}

// dailyFiles matches the daily files a program and its server log to.
const dailyFiles = "*-[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9].log"

// LogLines reads the lines logged in dir, checking each as it first comes;
// args name the run in a failure.
func LogLines(t *testing.T, dir string, args []string) []LogLine {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, dailyFiles))
	if err != nil || len(files) == 0 {
		t.Fatalf("%v: no log file in %s", args, dir)
	}
	cached, known := logsRead.LoadOrStore(dir, &dirLogs{files: map[string]*logFile{}})
	if !known {
		t.Cleanup(func() { logsRead.Delete(dir) })
	}
	logs := cached.(*dirLogs)
	logs.mu.Lock()
	defer logs.mu.Unlock()
	var lines []LogLine
	for _, path := range files {
		f := logs.files[path]
		if f == nil {
			f = &logFile{}
			logs.files[path] = f
		}
		f.readMore(t, path, args)
		lines = append(lines, f.lines...)
	}
	return lines
}

// readMore parses the lines added whole to the log file at path since the
// last read. A line still being written is left for the next read: a read can
// see part of a write, as when the line crosses a page.
func (f *logFile) readMore(t *testing.T, path string, args []string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Seek(f.read, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	b = b[:bytes.LastIndexByte(b, '\n')+1]
	f.read += int64(len(b))
	for raw := range strings.Lines(string(b)) {
		var l LogLine
		if err := json.Unmarshal([]byte(raw), &l); err != nil || l.Time == "" || l.Level == "" || l.Msg == "" || l.DebugID == "" || l.Process == "" {
			t.Fatalf("%v: bad log line %q: %v", args, raw, err)
		}
		f.lines = append(f.lines, l)
	}
}

// runLogged returns the program's lines of the run id, how many requests it
// says it made and had answered, how many it gave up on, and how many the
// server says it served under it.
func runLogged(logs Logged, lines []LogLine, id string) (run []LogLine, called, abandoned, served int) {
	for _, l := range lines {
		// A view's version check logs at debug level on both sides, where a
		// program's log level may not reach while the server's does.
		if l.DebugID != id || l.Level == "DEBUG" && l.Path == "/v1/version" {
			continue
		}
		switch {
		case l.Process == "cli":
			run = append(run, l)
			switch l.Msg {
			case logs.Called:
				called++
			case logs.Abandoned:
				abandoned++
			}
		case l.Msg == logs.Served:
			served++
		}
	}
	return run, called, abandoned, served
}

var escapes = regexp.MustCompile("\x1b\\[[0-9;]*m")

// WantLogged checks that the run of args that printed out is logged in
// logs.Dir from the command line it received to the response it returned,
// with the server's lines for its requests under the same debug id, and that
// every line in the log is a JSON object tagged with a debug id and a
// process. redact, when not nil, changes the JSON printed into what the run
// logs of it, for what it measures instead of logging. A home, Dir's parent,
// that does not exist has nowhere to log: the run must still work, which its
// test checks.
func WantLogged(t *testing.T, logs Logged, args []string, out Output, redact func(printed map[string]any)) {
	t.Helper()
	if info, err := os.Stat(filepath.Dir(logs.Dir)); err != nil || !info.IsDir() {
		return
	}
	lines := LogLines(t, logs.Dir, args)
	// Runs of the same command line in one home are sequential, so the last
	// one received is this one; a server started in the home runs others.
	pid := cmp.Or(out.Pid, os.Getpid())
	var id string
	for _, l := range lines {
		if l.Msg != "command received" || l.Pid != pid {
			continue
		}
		var received []string
		if json.Unmarshal(l.Args, &received) == nil && slices.Equal(received, clikit.ArgsLogged(args)) {
			id = l.DebugID
		}
	}
	var run []LogLine
	var called, abandoned, served int
	// A server learns that a client has left, as tide leaves its stream, only
	// after the client has gone, and logs that request's end then. A request
	// the client gave up on the server may have served or never received.
	logged := func() bool { return served >= called && served <= called+abandoned }
	waittest.Until(t, func() bool {
		run, called, abandoned, served = runLogged(logs, LogLines(t, logs.Dir, args), id)
		return logged()
	})
	if len(run) == 0 {
		t.Fatalf("%v: nothing logged under %q", args, id)
	}
	first, last := run[0], run[len(run)-1]
	if first.Msg != "command received" || last.Msg != "command returned" {
		t.Fatalf("%v: want the run logged from received to returned, got %q to %q", args, first.Msg, last.Msg)
	}
	if !logged() {
		t.Fatalf("%v: the server logged %d requests under %s, the program %d answered and %d given up on", args, served, id, called, abandoned)
	}
	if last.Exit == nil || *last.Exit != out.Exit || (last.Level == "ERROR") != (out.Exit != clikit.ExitOK) {
		t.Fatalf("%v: logged exit %v at %s, want exit %d", args, last.Exit, last.Level, out.Exit)
	}
	if string(last.Response) == "null" || len(last.Response) == 0 {
		t.Fatalf("%v: no response logged", args)
	}
	if strings.Contains(string(last.Response), `\u001b`) {
		t.Fatalf("%v: want the response logged without color, got %s", args, last.Response)
	}
	var printedCode struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(last.Response, &printedCode) // a completion logs text, without a code
	if last.Code != printedCode.Code {
		t.Fatalf("%v: logged code %q, want the response's %q", args, last.Code, printedCode.Code)
	}
	if slices.Contains(args, "--json") {
		var logged, printed any
		if err := json.Unmarshal([]byte(out.Stdout), &printed); err != nil {
			t.Fatalf("%v: stdout is not one JSON object: %s", args, out.Stdout)
		}
		_ = json.Unmarshal(last.Response, &logged) // compared to stdout, checked to be JSON
		if obj, ok := printed.(map[string]any); ok && redact != nil {
			redact(obj)
		}
		if !reflect.DeepEqual(logged, printed) {
			t.Fatalf("%v: logged response differs from stdout:\n%s\n%s", args, last.Response, out.Stdout)
		}
		if out.Exit != clikit.ExitOK && !strings.Contains(out.Stdout, `"debug_id": "`+id+`"`) {
			t.Fatalf("%v: want debug_id %s in the error, got %s", args, id, out.Stdout)
		}
	} else if out.Exit != clikit.ExitOK && !strings.HasSuffix(escapes.ReplaceAllString(out.Stderr, ""), "\n  debug_id: "+id+"\n") {
		t.Fatalf("%v: want debug_id %s on the last line of the error, got %q", args, id, out.Stderr)
	}
}

// JSON checks that the run of args with --json that printed out printed
// exactly one JSON object on stdout and nothing on stderr: a problem exactly
// when the run failed, the problem of its code as codes describes it. It
// returns the object.
func JSON(t *testing.T, args []string, out Output, codes func(code string) (clikit.CodeInfo, bool)) []byte {
	t.Helper()
	if out.Stderr != "" {
		t.Fatalf("%v: want empty stderr, got %q", args, out.Stderr)
	}
	var v struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
		Code   string `json:"code"`
	}
	dec := json.NewDecoder(strings.NewReader(out.Stdout))
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("%v: stdout is not JSON: %v\n%s", args, err, out.Stdout)
	}
	if dec.More() {
		t.Fatalf("%v: want one JSON object, got more:\n%s", args, out.Stdout)
	}
	if failed := out.Exit != clikit.ExitOK; failed != (v.Code != "") {
		t.Fatalf("%v: exit %d and code %q disagree", args, out.Exit, v.Code)
	}
	if v.Code != "" {
		info, _ := codes(v.Code)
		if v.Type != "urn:wdx:error:"+v.Code || v.Title != info.Description || v.Status != info.Status || v.Detail == "" || info.Exit != out.Exit {
			t.Fatalf("%v: not the problem of %s: %s", args, v.Code, out.Stdout)
		}
	}
	return []byte(out.Stdout)
}

// WantDeclared checks that the help of the command of root args name lists
// result and codes, those the run returned; "" stands for none.
func WantDeclared(t *testing.T, root *cobra.Command, args []string, result string, codes ...string) {
	t.Helper()
	cmd, _, err := root.Find(args)
	if err != nil {
		return
	}
	if result != "" && !slices.Contains(clikit.AnnotationList(cmd, clikit.Results), result) {
		t.Fatalf("%v: result %q is not in the help of %s", args, result, cmd.CommandPath())
	}
	for _, code := range codes {
		if code != "" && !slices.Contains(clikit.ErrorList(cmd), code) {
			t.Fatalf("%v: error %q is not in the help of %s", args, code, cmd.CommandPath())
		}
	}
}

// CheckDocs checks that every command of root documents itself, so that a
// new command fails until its help is complete.
func CheckDocs(t testing.TB, root *cobra.Command) {
	t.Helper()
	var check func(cmd *cobra.Command)
	check = func(cmd *cobra.Command) {
		path := cmd.CommandPath()
		if cmd.Short == "" {
			t.Errorf("%s: no summary", path)
		}
		leaf := !cmd.HasAvailableSubCommands()
		if leaf && cmd.Annotations[clikit.OutputNote] == "" {
			t.Errorf("%s: no output note", path)
		}
		for _, code := range clikit.AnnotationList(cmd, clikit.Errors) {
			if code == clikit.CodeUsage || code == clikit.CodeInternal {
				t.Errorf("%s: lists %q, which every command can return", path, code)
			}
		}
		if errs := clikit.ErrorList(cmd); leaf && (len(errs) < 2 || errs[len(errs)-2] != clikit.CodeUsage || errs[len(errs)-1] != clikit.CodeInternal) {
			t.Errorf("%s: errors must end with usage and internal: %v", path, errs)
		}
		if leaf && len(clikit.ParseExamples(cmd.Example)) == 0 {
			t.Errorf("%s: no examples", path)
		}
		flags := cmd.LocalFlags()
		if !cmd.HasParent() {
			flags = cmd.PersistentFlags()
		}
		flags.VisitAll(func(f *pflag.Flag) {
			if f.Value.Type() == "bool" {
				return
			}
			if name, _ := pflag.UnquoteUsage(f); !strings.Contains(f.Usage, "`"+name+"`") {
				t.Errorf("%s: --%s does not name its value with backticks in %q", path, f.Name, f.Usage)
			}
			if _, ok := clikit.FlagCompletion(cmd, f.Name); !ok {
				t.Errorf("%s: --%s completes nothing; register its completion, cobra.NoFileCompletions if none", path, f.Name)
			}
		})
		if cmd.Runnable() && cmd.ValidateArgs([]string{"x"}) == nil {
			t.Errorf("%s: takes a positional argument; every input must be a flag", path)
		}
		for _, r := range clikit.FlagRules(cmd) {
			names := r.Flags
			if r.When != nil {
				names = append(names, *r.When)
			}
			if r.Unless != nil {
				names = append(names, *r.Unless)
			}
			for _, name := range names {
				if cmd.Flags().Lookup(name) == nil {
					t.Errorf("%s: rule %s names unknown flag %q", path, r.Rule, name)
				}
			}
		}
		for _, sub := range cmd.Commands() {
			if sub.IsAvailableCommand() {
				check(sub)
			}
		}
	}
	check(root)
}

// CheckExamples checks every example of the tree newRoot builds is a command
// line its command accepts: it names the command, and its flags parse and
// follow the command's rules.
func CheckExamples(t testing.TB, newRoot func() *cobra.Command) {
	t.Helper()
	var check func(cmd *cobra.Command)
	check = func(cmd *cobra.Command) {
		for _, e := range clikit.ParseExamples(cmd.Example) {
			c, err := CheckCommandLine(newRoot, e.Command)
			switch {
			case err != nil:
				t.Errorf("%s: example %q: %v", cmd.CommandPath(), e.Command, err)
			case c.CommandPath() != cmd.CommandPath():
				t.Errorf("%s: example %q runs %v", cmd.CommandPath(), e.Command, c.CommandPath())
			}
		}
		for _, sub := range cmd.Commands() {
			if sub.IsAvailableCommand() {
				check(sub)
			}
		}
	}
	check(newRoot())
}

// CheckCommandLine returns the command line names in the tree newRoot
// builds, and an error unless it is one that command accepts: free of shell
// quoting, with flags that parse and follow its rules.
func CheckCommandLine(newRoot func() *cobra.Command, line string) (*cobra.Command, error) {
	if strings.ContainsAny(line, `'"\`) {
		return nil, errors.New("it quotes; keep command lines free of shell quoting")
	}
	args := strings.Fields(line)
	// A fresh tree, since parsing sets the flags.
	c, rest, err := newRoot().Find(args[1:])
	if err != nil {
		return nil, err
	}
	for _, validate := range []func() error{
		func() error { return c.ParseFlags(rest) },
		func() error { return c.ValidateArgs(c.Flags().Args()) },
		c.ValidateRequiredFlags,
		c.ValidateFlagGroups,
		func() error { return clikit.CheckJSONRequires(c) },
	} {
		if err := validate(); err != nil {
			return c, err
		}
	}
	return c, nil
}

// Typed returns a terminal for a command's stdin, with answers already typed
// into it, one per line.
func Typed(t *testing.T, answers ...string) *os.File {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		slave.Close()
		master.Close()
	})
	go func() { _, _ = io.Copy(io.Discard, master) }() // the echo
	for _, a := range answers {
		if _, err := master.WriteString(a + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	return slave
}
