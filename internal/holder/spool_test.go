package holder

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestSpool(t *testing.T, segment, backlog int64) *spool {
	t.Helper()
	s, err := openSpool(filepath.Join(t.TempDir(), "spool"), segment, backlog, spoolKeep)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	return s
}

// readAll reads the spool from pos to its end in reads of size bytes.
func readAll(t *testing.T, s *spool, pos int64, size int) (from int64, out string) {
	t.Helper()
	from = -1
	buf := make([]byte, size)
	var b bytes.Buffer
	for {
		at, n, err := s.read(pos, buf)
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
		if from < 0 {
			from = at
		}
		if n == 0 {
			return from, b.String()
		}
		b.Write(buf[:n])
		pos = at + int64(n)
	}
}

func TestSpoolSegments(t *testing.T) {
	t.Parallel()
	s := newTestSpool(t, 10, 1000)
	want := "the output of a shell, spread over several segments"
	for _, part := range []string{want[:3], want[3:25], want[25:]} {
		if err := s.append([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if s.position() != int64(len(want)) {
		t.Fatalf("position %d, want %d", s.position(), len(want))
	}
	for _, size := range []int{1, 4, 64} {
		if from, got := readAll(t, s, 0, size); from != 0 || got != want {
			t.Fatalf("reads of %d: from %d got %q", size, from, got)
		}
	}
	if from, got := readAll(t, s, 17, 64); from != 17 || got != want[17:] {
		t.Fatalf("from 17: from %d got %q", from, got)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 6 || entries[0].Name() != "0000000000000000" || entries[5].Name() != "0000000000000050" {
		t.Fatalf("got segments %v", entries)
	}
	for _, e := range entries {
		if info, err := e.Info(); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: mode %v, %v", e.Name(), info.Mode(), err)
		}
	}
	if info, err := os.Stat(s.dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode %v, %v", info.Mode(), err)
	}
}

func TestSpoolConsumed(t *testing.T) {
	t.Parallel()
	s := newTestSpool(t, 10, 1000)
	if err := s.append([]byte(strings.Repeat("x", 50))); err != nil {
		t.Fatal(err)
	}
	s.consume(50)
	// The last two segments stay for clients reading after a snapshot.
	if from, got := readAll(t, s, 0, 64); from != 30 || len(got) != 20 {
		t.Fatalf("got %d bytes from %d, want the last 20 from 30", len(got), from)
	}
}

func TestSpoolBacklog(t *testing.T) {
	t.Parallel()
	s := newTestSpool(t, 10, 25)
	if err := s.append([]byte(strings.Repeat("x", 60))); err != nil {
		t.Fatal(err)
	}
	// Nothing consumed: the oldest segments go once more than 25 bytes
	// would be kept without them.
	if from, got := readAll(t, s, 0, 64); from != 30 || len(got) != 30 {
		t.Fatalf("got %d bytes from %d, want 30 from 30", len(got), from)
	}
}

func TestSpoolWaitAndFinish(t *testing.T) {
	t.Parallel()
	s := newTestSpool(t, 10, 1000)
	wait := s.wait()
	if err := s.append([]byte("a")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wait:
	default:
		t.Fatal("want an append to wake waiters")
	}
	buf := make([]byte, 8)
	if _, n, err := s.read(1, buf); n != 0 || err != nil {
		t.Fatalf("at the end before finish: %d, %v", n, err)
	}
	wait = s.wait()
	s.finish()
	select {
	case <-wait:
	default:
		t.Fatal("want finish to wake waiters")
	}
	if _, n, err := s.read(0, buf); n != 1 || err != nil {
		t.Fatalf("after finish: %d, %v, want what is left", n, err)
	}
	if _, _, err := s.read(1, buf); !errors.Is(err, io.EOF) {
		t.Fatalf("at the end after finish: %v, want EOF", err)
	}
	if err := s.append([]byte("b")); err == nil {
		t.Fatal("want appending after finish refused")
	}
}

func TestSpoolReopened(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "spool")
	s, err := openSpool(dir, 10, 1000, spoolKeep)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.append([]byte(strings.Repeat("x", 25))); err != nil {
		t.Fatal(err)
	}
	s.close()
	s, err = openSpool(dir, 10, 1000, spoolKeep)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if s.position() != 0 {
		t.Fatalf("position %d, want a new run to start empty", s.position())
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("got %v, %v, want one empty segment", entries, err)
	}
}
