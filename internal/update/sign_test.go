package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// signedMark is what the fake signer appends to the binary it signs, so a
// test can see which binary was installed.
const signedMark = "# signed\n"

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
		{name: "certificate made", made: true, wantSigned: true, wantWarning: "a new signing certificate was made: grant tool Full Disk Access once"},
		{name: "signing fails", fail: errors.New("codesign: no identity found"), wantWarning: "tool 0.9.0 is installed unsigned: codesign: no identity found; macOS will ask for its permissions again"},
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
			if len(signed) != 1 || filepath.Base(signed[0]) == o.Program.Name {
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
			if tc.made && !strings.Contains(r.Warnings[0], "adding "+o.Program.Binary) {
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
