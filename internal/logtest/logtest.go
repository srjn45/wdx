// Package logtest shows a test's logs when it fails: the lines the daemon,
// the CLI and the holders wrote to a home's log files, so a failure says
// what they did without running the test again.
package logtest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/identity"
)

// kept is how many of the last lines a failure shows.
const kept = 80

// width cuts a line shown, as a response logged whole can be long.
const width = 400

// OnFailure shows the end of home's logs once t has failed. Call it before
// starting what logs, so it runs after their cleanups have stopped them.
func OnFailure(t *testing.T, home string) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		lines, err := Tail(home, kept)
		if err != nil {
			t.Logf("logs of %s not read: %v", home, err)
			return
		}
		t.Logf("the last %d lines logged in %s:\n%s", len(lines), identity.LogsDir(home), strings.Join(lines, "\n"))
	})
}

// Tail returns the last n lines of home's log files, oldest first, each
// shortened to its time, level, message and fields.
func Tail(home string, n int) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(identity.LogsDir(home), "*.log"))
	if err != nil {
		return nil, err
	}
	slices.Sort(files) // daily files: by name is by day
	var lines []string
	for _, f := range files {
		file, err := os.Open(f)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(file)
		sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
		for sc.Scan() {
			lines = append(lines, short(sc.Text()))
			if len(lines) > n {
				lines = lines[1:]
			}
		}
		err = sc.Err()
		file.Close()
		if err != nil {
			return nil, err
		}
	}
	return lines, nil
}

// short is a JSON log line as "15:04:05.000 INFO msg key=value ...".
func short(line string) string {
	var fields map[string]any
	if json.Unmarshal([]byte(line), &fields) != nil {
		return cut(line)
	}
	at, _ := fields["time"].(string)
	if _, clock, ok := strings.Cut(at, "T"); ok {
		at = strings.TrimSuffix(clock, "Z")
		at = at[:min(len(at), len("15:04:05.000"))]
	}
	b := fmt.Sprintf("%s %v %v", at, fields["level"], fields["msg"])
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if k != "time" && k != "level" && k != "msg" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		v, _ := json.Marshal(fields[k])
		b += " " + k + "=" + string(v)
	}
	return cut(b)
}

func cut(s string) string {
	if len(s) > width {
		return s[:width] + "…"
	}
	return s
}
