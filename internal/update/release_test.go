package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/srjn45/wdx/internal/identity"
)

func TestChecksum(t *testing.T) {
	t.Parallel()
	sums := []byte("aaa  wdx_0.2.0_linux_amd64.tar.gz\nbbb  wdx_0.2.0_darwin_arm64.tar.gz\n")
	for _, tc := range []struct {
		file, want string
		ok         bool
	}{
		{"wdx_0.2.0_linux_amd64.tar.gz", "aaa", true},
		{"wdx_0.2.0_darwin_arm64.tar.gz", "bbb", true},
		{"wdx_0.2.0_linux_arm64.tar.gz", "", false},
		{"wdx_0.2.0_linux_amd64", "", false},
	} {
		if got, ok := checksum(sums, tc.file); got != tc.want || ok != tc.ok {
			t.Errorf("%s: got %q, %v; want %q, %v", tc.file, got, ok, tc.want, tc.ok)
		}
	}
	if _, ok := checksum([]byte("aaa wdx.tar.gz\n"), "wdx.tar.gz"); ok {
		t.Error("want a line with one space, which sha256sum never writes, not matched")
	}
}

// archiveOf is a tar.gz holding files, by name.
func archiveOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestExtract(t *testing.T) {
	t.Parallel()
	dest := filepath.Join(t.TempDir(), "wdx")
	archive := archiveOf(t, map[string]string{"LICENSE": "license", "wdx": "binary"})
	if err := extract(bytes.NewReader(archive), "wdx", dest); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "binary" || info.Mode().Perm() != 0o755 {
		t.Fatalf("got %q, %v; want the binary, executable", b, info.Mode().Perm())
	}

	if err := extract(bytes.NewReader(archiveOf(t, map[string]string{"LICENSE": "license"})), "wdx", dest); err == nil {
		t.Error("want an archive without the binary refused")
	}
	if err := extract(bytes.NewReader([]byte("not gzip")), "wdx", dest); err == nil {
		t.Error("want what is not a tar.gz refused")
	}
}

func TestComparePre(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		a, b []string
		want int
	}{
		{nil, nil, 0},
		{nil, []string{"dev", "1"}, 1}, // a release comes after its pre-releases
		{[]string{"dev", "1"}, nil, -1},
		{[]string{"dev", "2"}, []string{"dev", "10"}, -1}, // numbers compare as numbers
		{[]string{"dev", "10"}, []string{"dev", "2"}, 1},
		{[]string{"1"}, []string{"alpha"}, -1}, // a number comes before a word
		{[]string{"alpha"}, []string{"1"}, 1},
		{[]string{"alpha"}, []string{"beta"}, -1},
		{[]string{"alpha"}, []string{"alpha", "1"}, -1}, // more identifiers come after
		{[]string{"dev", "3"}, []string{"dev", "3"}, 0},
	} {
		if got := comparePre(tc.a, tc.b); got != tc.want {
			t.Errorf("comparePre(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestLatestGitHub asks GitHub's API, served in the test, for the newest
// release of each channel.
func TestLatestGitHub(t *testing.T) {
	t.Parallel()
	answers := map[string]string{
		"/releases/latest":        `{"tag_name": "v0.3.0"}`,
		"/releases?per_page=1":    `[{"tag_name": "v0.4.0-dev.2"}]`,
		"/no-releases?per_page=1": `[]`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		answer, ok := answers[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(answer))
	}))
	defer srv.Close()
	r := releases{base: identity.ReleasesURL, api: srv.URL + "/releases", http: srv.Client()}
	ctx := context.Background()
	for channel, want := range map[string]string{"stable": "0.3.0", "dev": "0.4.0-dev.2"} {
		if got, err := r.latest(ctx, channel); err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", channel, got, err, want)
		}
	}

	r.api = srv.URL + "/no-releases"
	_, err := r.latest(ctx, "dev")
	if _, ok := errors.AsType[*DownloadError](err); !ok {
		t.Fatalf("want a DownloadError for no release, got %v", err)
	}
}
