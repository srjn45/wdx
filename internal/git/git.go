// Package git wraps the git CLI.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/logging"
)

// ErrNotRepo is returned when a directory is not inside a git work tree.
var ErrNotRepo = errors.New("not a git repository")

// TopLevel returns the root of the git work tree containing dir.
func TopLevel(ctx context.Context, dir string) (string, error) {
	out, err := run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); ok {
			return "", ErrNotRepo
		}
		return "", err
	}
	return out, nil
}

// CommonDir returns the absolute git directory shared by all worktrees of the
// repository containing dir.
func CommonDir(ctx context.Context, dir string) (string, error) {
	return run(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
}

// RemoteURL returns the URL of the named remote, or "" if it is not configured.
func RemoteURL(ctx context.Context, dir, remote string) (string, error) {
	out, err := run(ctx, dir, "remote", "get-url", remote)
	if err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); ok {
			return "", nil
		}
		return "", err
	}
	return out, nil
}

// CloneError is returned when git fails to clone URL. Output is what git
// printed on stderr, which Clone has already written to its progress writer.
type CloneError struct {
	URL    string
	Output string
	Err    error
}

func (e *CloneError) Error() string {
	return fmt.Sprintf("git clone %s failed", e.URL)
}

func (e *CloneError) Unwrap() error { return e.Err }

// Clone clones url into dest, writing git's output to progress. git
// removes what it made of dest when it fails, but not when it is killed.
func Clone(ctx context.Context, url, dest string, progress io.Writer) error {
	log := logging.FromContext(ctx).With("url", url, "dest", dest)
	log.Info("cloning")
	start := time.Now()
	cmd := exec.CommandContext(ctx, "git", "clone", "--", url, dest)
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = progress, io.MultiWriter(progress, &stderr)
	if err := cmd.Run(); err != nil {
		e := &CloneError{URL: url, Output: strings.TrimSpace(stderr.String()), Err: err}
		log.Error("clone failed", "err", err.Error(), "output", e.Output, logging.Duration(start))
		return e
	}
	log.Info("cloned", logging.Duration(start))
	return nil
}

// Init creates an empty git repository in dir.
func Init(ctx context.Context, dir string) error {
	if _, err := run(ctx, dir, "init", "-q"); err != nil {
		return fmt.Errorf("git init %s: %w", dir, err)
	}
	return nil
}

// RepoKey normalizes a remote URL so that different spellings of the same
// repository compare equal: scheme, user, port, host case and a trailing
// ".git" or "/" are ignored. An empty URL yields "".
func RepoKey(url string) string {
	u := strings.TrimSpace(url)
	if u == "" {
		return ""
	}
	var host, path string
	if _, rest, ok := strings.Cut(u, "://"); ok {
		host, path, _ = strings.Cut(rest, "/")
		if h, _, err := net.SplitHostPort(host[strings.LastIndex(host, "@")+1:]); err == nil {
			host = h
		}
	} else if h, p, ok := strings.Cut(u, ":"); ok && !strings.Contains(h, "/") {
		host, path = h, p // scp-like: [user@]host:path
	} else {
		return strings.TrimSuffix(filepath.Clean(u), ".git") // local path
	}
	host = strings.ToLower(host[strings.LastIndex(host, "@")+1:])
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	return host + "/" + strings.Trim(path, "/")
}

// run runs git in dir and returns its trimmed stdout. A non-zero exit is often
// an answer, such as "not a repository", so it is logged as info and left to
// the caller to judge.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	log := logging.FromContext(ctx).With("dir", dir, "args", args)
	start := time.Now()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if e, ok := errors.AsType[*exec.ExitError](err); ok {
		log.Info("git ran", "exit", e.ExitCode(), "stderr", strings.TrimSpace(stderr.String()), logging.Duration(start))
		return "", err
	}
	if err != nil {
		log.Error("git could not run", "err", err.Error(), logging.Duration(start))
		return "", fmt.Errorf("run git: %w", err)
	}
	out := strings.TrimSpace(stdout.String())
	log.Info("git ran", "exit", 0, logging.Duration(start))
	log.Debug("git output", "stdout", out)
	return out, nil
}
