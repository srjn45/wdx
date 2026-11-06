package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/runtest"
)

func TestMain(m *testing.M) { os.Exit(runtest.Main(m)) }

// release is a wdx that says it is version 0.9.0, which preflight checks.
const release = "#!/bin/sh\necho '{\"cli\": {\"name\": \"wdx\", \"version\": \"0.9.0\", \"commit\": \"none\"}, \"daemon\": null}'\n"

// signedMark is what the fake signer appends to the binary it signs, so a
// test can see which binary was installed.
const signedMark = "# signed\n"

// applying returns the options of an update from version 0.1.0 to the
// release, in a home of its own, with a daemon that answers as the
// version installed.
func applying(t *testing.T) Options {
	t.Helper()
	base := t.TempDir()
	name := archive("0.9.0")
	content := archiveOf(t, map[string]string{identity.Name: release})
	sum := sha256.Sum256(content)
	for path, data := range map[string]string{
		filepath.Join(base, "LATEST"):                  "v0.9.0\n",
		filepath.Join(base, "v0.9.0", name):            string(content),
		filepath.Join(base, "v0.9.0", "checksums.txt"): hex.EncodeToString(sum[:]) + "  " + name + "\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	home := t.TempDir()
	binary := filepath.Join(t.TempDir(), identity.Name)
	if err := runtest.WriteExecutable(binary, "#!/bin/sh\necho old\n"); err != nil {
		t.Fatal(err)
	}
	o := Options{Home: home, Binary: binary, Current: "0.1.0", BaseURL: "file://" + base}
	o.Daemon = Daemon{
		Stop:  func(context.Context) error { return nil },
		Start: func(context.Context) (string, error) { return installedVersion(t, o.Binary), nil },
	}
	return o
}

// installedVersion is the version the installed binary says it is, as the
// daemon it starts would answer.
func installedVersion(t *testing.T, binary string) string {
	t.Helper()
	b, err := os.ReadFile(binary)
	if err != nil {
		t.Error(err)
		return ""
	}
	if strings.Contains(string(b), `"version": "0.9.0"`) {
		return "0.9.0"
	}
	return "0.1.0"
}

func installed(t *testing.T, o Options) string {
	t.Helper()
	b, err := os.ReadFile(o.Binary)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplySigns(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		made        bool
		fail        error
		wantSigned  bool
		wantWarning string
	}{
		{name: "signed", wantSigned: true},
		{name: "certificate made", made: true, wantSigned: true, wantWarning: "a new signing certificate was made: grant wdx Full Disk Access once"},
		{name: "signing fails", fail: errors.New("codesign: no identity found"), wantWarning: "wdx 0.9.0 is installed unsigned: codesign: no identity found; macOS will ask for its permissions again"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := applying(t)
			var signed []string
			o.Sign = func(_ context.Context, binary string) (bool, error) {
				signed = append(signed, binary)
				if tc.fail != nil {
					return false, tc.fail
				}
				f, err := os.OpenFile(binary, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					return false, err
				}
				defer f.Close()
				_, err = f.WriteString(signedMark)
				return tc.made, err
			}

			r, err := Apply(context.Background(), o, "stable", "")
			if err != nil || r.To != "0.9.0" {
				t.Fatalf("want updated to 0.9.0, got %+v, %v", r, err)
			}
			if len(signed) != 1 || filepath.Base(signed[0]) == identity.Name {
				t.Fatalf("want one copy of the download signed, got %q", signed)
			}
			got := installed(t, o)
			if !strings.HasPrefix(got, release) || strings.HasSuffix(got, signedMark) != tc.wantSigned {
				t.Fatalf("want the release installed, signed %v, got\n%s", tc.wantSigned, got)
			}
			switch {
			case tc.wantWarning == "" && len(r.Warnings) != 0:
				t.Fatalf("want no warnings, got %q", r.Warnings)
			case tc.wantWarning != "" && (len(r.Warnings) != 1 || !strings.HasPrefix(r.Warnings[0], tc.wantWarning)):
				t.Fatalf("want the warning %q, got %q", tc.wantWarning, r.Warnings)
			}
			if tc.made && !strings.Contains(r.Warnings[0], "adding "+o.Binary) {
				t.Fatalf("want the installed path in the warning, got %q", r.Warnings[0])
			}
		})
	}
}

// TestApplyUnsigned installs the release as it comes when nothing signs it,
// as on Linux.
func TestApplyUnsigned(t *testing.T) {
	t.Parallel()
	o := applying(t)
	r, err := Apply(context.Background(), o, "stable", "")
	if err != nil || r.To != "0.9.0" || len(r.Warnings) != 0 {
		t.Fatalf("want updated to 0.9.0 with no warnings, got %+v, %v", r, err)
	}
	if got := installed(t, o); got != release {
		t.Fatalf("want the release as it came, got\n%s", got)
	}
}
