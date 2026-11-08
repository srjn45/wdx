package sqlite_test

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite"
)

// A Secret reaches the database whole and the log as its length.
func TestExecLogsASecretsLength(t *testing.T) {
	t.Parallel()
	database, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "wdx.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err := database.Exec("CREATE TABLE t (s TEXT, b BLOB) STRICT"); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	ctx := logging.NewContext(t.Context(), logging.New(&log, slog.LevelDebug))
	_, err = sqlite.Exec(ctx, database, "INSERT INTO t (s, b) VALUES (?, ?)", sqlite.Secret[string]{V: "hunter2-text"}, sqlite.Secret[[]byte]{V: []byte("hunter2-bytes")})
	if err != nil {
		t.Fatal(err)
	}
	var s string
	var b []byte
	if err := database.QueryRow("SELECT s, b FROM t").Scan(&s, &b); err != nil || s != "hunter2-text" || string(b) != "hunter2-bytes" {
		t.Fatalf("stored %q, %q, %v; want the secrets whole", s, b, err)
	}
	line := log.String()
	// The bytes would be logged in base64: aHVudGVyMi1ieXRlcw.
	if strings.Contains(line, "hunter2") || strings.Contains(line, "aHVudGVyMi1ieXRlcw") {
		t.Errorf("a secret is in the log: %s", line)
	}
	if !strings.Contains(line, `"[secret, 12 bytes]"`) || !strings.Contains(line, `"[secret, 13 bytes]"`) {
		t.Errorf("the secrets' lengths are not in the log: %s", line)
	}
}
