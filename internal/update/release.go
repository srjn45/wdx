package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// downloadTimeout bounds every request to the releases, a download included.
const downloadTimeout = 5 * time.Minute

// DownloadError is a release that could not be found or downloaded.
type DownloadError struct {
	URL string
	Err error
}

func (e *DownloadError) Error() string { return fmt.Sprintf("download %s: %v", e.URL, e.Err) }
func (e *DownloadError) Unwrap() error { return e.Err }

// ChecksumError is a downloaded archive that does not match checksums.txt.
type ChecksumError struct {
	Archive string
}

func (e *ChecksumError) Error() string {
	return e.Archive + " does not match its checksum in checksums.txt"
}

// releases are the releases published under base, laid out as GitHub
// Releases: <base>/v<version>/<archive> and checksums.txt.
type releases struct {
	base      string
	api       string // GitHub's answer to the latest stable release, when base is GitHub's
	devLatest string // the file naming the newest of either kind, when base is GitHub's
	http      *http.Client
}

func newReleases(base string) releases {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.RegisterProtocol("file", http.NewFileTransport(http.Dir("/")))
	return releases{
		base:      base,
		api:       identity.ReleasesAPI,
		devLatest: identity.DevLatestURL,
		http:      &http.Client{Transport: transport, Timeout: downloadTimeout},
	}
}

// latest returns the newest release on channel: the newest stable one, or
// for dev the newest of either kind. On GitHub, stable comes from its API and
// dev from the file scripts/publish-latest.sh moves. Any other base holds the
// tag in LATEST and LATEST-dev, as scripts/dist.sh writes them.
func (r releases) latest(ctx context.Context, channel string) (string, error) {
	var url, tag string
	var err error
	switch {
	case r.base != identity.ReleasesURL:
		url = r.base + "/LATEST"
		if channel == "dev" {
			url += "-dev"
		}
		tag, err = r.getTag(ctx, url)
	case channel == "dev":
		url = r.devLatest
		tag, err = r.getTag(ctx, url)
	default:
		url = r.api + "/latest"
		tag, err = r.getLatestAPI(ctx, url)
	}
	if err != nil {
		return "", err
	}
	// Anything else would read as older than the installed version, and so
	// as up to date.
	if _, ok := parseVersion(tag); !ok {
		return "", &DownloadError{URL: url, Err: fmt.Errorf("no release in the answer: %q", tag)}
	}
	logging.FromContext(ctx).Info("latest release found", "channel", channel, "tag", tag, "base", r.base)
	return strings.TrimPrefix(tag, "v"), nil
}

func (r releases) getTag(ctx context.Context, url string) (string, error) {
	body, err := r.get(ctx, url)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

func (r releases) getLatestAPI(ctx context.Context, url string) (string, error) {
	body, err := r.get(ctx, url)
	if err != nil {
		return "", err
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", &DownloadError{URL: url, Err: fmt.Errorf("no release in the answer: %s", body)}
	}
	return release.TagName, nil
}

// archive is the name of the release archive of version for this machine.
func archive(version string) string {
	return fmt.Sprintf("%s_%s_%s_%s.tar.gz", identity.Name, version, runtime.GOOS, runtime.GOARCH)
}

// exists fails when version has no checksums.txt, so no release.
func (r releases) exists(ctx context.Context, version string) error {
	_, err := r.get(ctx, r.base+"/v"+version+"/checksums.txt")
	return err
}

// download writes the binary of release version to dir/wdx, once its
// archive matches its checksum, and returns its path.
func (r releases) download(ctx context.Context, version, dir string) (string, error) {
	name := archive(version)
	sums, err := r.get(ctx, r.base+"/v"+version+"/checksums.txt")
	if err != nil {
		return "", err
	}
	url := r.base + "/v" + version + "/" + name
	// Streamed to disk through the hash: the archive is never all in memory.
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if err := r.fetch(ctx, url, io.MultiWriter(f, hash)); err != nil {
		return "", err
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if want, ok := checksum(sums, name); !ok || want != got {
		logging.FromContext(ctx).Error("checksum mismatch", "archive", name, "want", want, "got", got)
		return "", &ChecksumError{Archive: name}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	path := filepath.Join(dir, identity.Name)
	if err := extract(f, identity.Name, path); err != nil {
		return "", fmt.Errorf("extract %s: %w", name, err)
	}
	logging.FromContext(ctx).Info("release downloaded", "version", version, "archive", url, "path", path)
	return path, nil
}

// checksum returns the sha256 of file in checksums.txt, as sha256sum writes
// them.
func checksum(sums []byte, file string) (string, bool) {
	for line := range strings.Lines(string(sums)) {
		if sum, name, ok := strings.Cut(strings.TrimSpace(line), "  "); ok && name == file {
			return sum, true
		}
	}
	return "", false
}

// extract writes the file name of the tar.gz archive to dest, executable.
func extract(archive io.Reader, name, dest string) error {
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("no %s in it", name)
		}
		if err != nil {
			return err
		}
		if h.Name != name {
			continue
		}
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	}
}

// get returns a small file of the releases: a tag or the checksums.
func (r releases) get(ctx context.Context, url string) ([]byte, error) {
	var body bytes.Buffer
	if err := r.fetch(ctx, url, &body); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

// fetch writes the file at url to w.
func (r releases) fetch(ctx context.Context, url string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return &DownloadError{URL: url, Err: err}
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return &DownloadError{URL: url, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &DownloadError{URL: url, Err: errors.New(resp.Status)}
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return &DownloadError{URL: url, Err: err}
	}
	return nil
}
