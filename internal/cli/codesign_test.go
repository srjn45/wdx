package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/identity"
)

// Signing runs only on macOS, which CI does not run: the install script's
// signing was checked by hand on a Mac (the wdx doc MAC_SIGNING).

func TestCodesignOffMacOS(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "darwin" {
		t.Skip("signs on macOS: checked by hand")
	}
	home := t.TempDir()
	binary := filepath.Join(t.TempDir(), "wdx")
	writeFile(t, binary, "")

	v, exit := wdxJSON(t, home, "codesign", "--path", binary)
	if exit != clikit.ExitUsage || v.Code != string(api.ErrorCodeUsage) || !strings.Contains(v.Detail, "macOS only") {
		t.Fatalf("want usage, macOS only, got exit %d, %+v", exit, v)
	}
	if _, err := os.Stat(identity.CodesignDir(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("want no keychain made off macOS")
	}

	out := wdx(t, home, "codesign", "--path", binary)
	if out.Exit != clikit.ExitUsage || !strings.Contains(out.Stderr, "wdx: not signed: signing is for macOS only") {
		t.Fatalf("want the text error, got exit %d, %q", out.Exit, out.Stderr)
	}
}
