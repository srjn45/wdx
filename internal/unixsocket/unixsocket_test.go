package unixsocket

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/runtest"
)

func TestMain(m *testing.M) { os.Exit(runtest.Main(m)) }

// The longest path Check lets through listens; one byte more is refused by
// the system as by Check.
func TestCheckIsTheSystemsLimit(t *testing.T) {
	t.Parallel()
	dir := runtest.Dir(t)
	longest := filepath.Join(dir, strings.Repeat("s", MaxPath-len(dir)-1))
	if err := Check(longest); err != nil {
		t.Fatalf("want %d bytes allowed, got %v", len(longest), err)
	}
	ln, err := net.Listen("unix", longest)
	if err != nil {
		t.Fatalf("the system refused %d bytes: %v", len(longest), err)
	}
	ln.Close()

	tooLong := longest + "s"
	err = Check(tooLong)
	if e, ok := errors.AsType[*TooLongError](err); !ok || e.Path != tooLong || e.Limit != MaxPath {
		t.Fatalf("want %d bytes refused, got %v", len(tooLong), err)
	}
	if !strings.Contains(err.Error(), tooLong) || !strings.Contains(err.Error(), fmt.Sprintf("is %d bytes, more than the %d", len(tooLong), MaxPath)) {
		t.Errorf("want the path and its length named: %v", err)
	}
	if ln, err := net.Listen("unix", tooLong); err == nil {
		ln.Close()
		t.Fatalf("the system took %d bytes, more than MaxPath", len(tooLong))
	}
}
