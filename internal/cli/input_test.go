package cli

import (
	"os"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/srjn45/wdx/internal/waittest"
)

// TestInputReaderClosedDuringARead is Bubble Tea's way out of a program: it
// cancels its input's reader, gives the read half a second to end, which a
// loaded machine may not, and closes the reader whether it ended or not. The
// race detector fails this if closing touches what the read is using.
func TestInputReaderClosedDuringARead(t *testing.T) {
	t.Parallel()
	in, typed, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close(); _ = typed.Close() }()
	if _, err := typed.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	reader, err := uv.NewCancelReader(in)
	if err != nil {
		t.Fatal(err)
	}
	reading, ended := make(chan struct{}), make(chan error)
	go func() {
		// Having read what was typed, it goes on to the read that closing meets.
		if _, err := reader.Read(make([]byte, 1)); err != nil {
			ended <- err
			return
		}
		close(reading)
		_, err := reader.Read(make([]byte, 1))
		ended <- err
	}()
	select {
	case <-reading:
	case err := <-ended:
		t.Fatalf("the first read: %v", err)
	case <-waittest.After(t):
		t.Fatal("the first read did not end")
	}
	if !reader.Cancel() {
		t.Fatal("not canceled")
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-ended:
		if err == nil {
			t.Fatal("the read returned nothing and no error")
		}
	case <-waittest.After(t):
		t.Fatal("the read did not end")
	}
}
