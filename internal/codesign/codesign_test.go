package codesign

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Signing itself runs only on macOS, which CI does not run: it was checked
// by hand on a Mac (the wdx doc MAC_SIGNING). These check what is read
// from security's output and what is logged, anywhere.

func TestSearchList(t *testing.T) {
	t.Parallel()
	out := []byte("    \"/Users/me/Library/Keychains/login.keychain-db\"\n    \"/Users/me/.wdx/codesign/wdx.keychain-db\"\n\n")
	want := []string{"/Users/me/Library/Keychains/login.keychain-db", "/Users/me/.wdx/codesign/wdx.keychain-db"}
	if got := searchList(out); !slices.Equal(got, want) {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestOnList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	kc := filepath.Join(link, "wdx.keychain-db")
	if err := os.WriteFile(filepath.Join(real, "wdx.keychain-db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		list []string
		want bool
	}{
		{"as given", []string{"/login.keychain-db", kc}, true},
		{"resolved", []string{filepath.Join(real, "wdx.keychain-db")}, true},
		{"absent", []string{"/login.keychain-db"}, false},
		{"empty", nil, false},
	} {
		if got := onList(tc.list, kc); got != tc.want {
			t.Errorf("%s: want %v, got %v", tc.name, tc.want, got)
		}
	}
}

func TestRedact(t *testing.T) {
	t.Parallel()
	k := &keychain{password: "s3cret"}
	got := k.redact([]string{"security", "unlock-keychain", "-p", "s3cret", "-passout", "pass:s3cret"})
	want := []string{"security", "unlock-keychain", "-p", "<password>", "-passout", "pass:<password>"}
	if !slices.Equal(got, want) {
		t.Fatalf("want %q, got %q", want, got)
	}
	if got := (&keychain{}).redact([]string{"a"}); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("with no password, want the args as they are, got %q", got)
	}
}
