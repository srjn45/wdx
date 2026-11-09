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

// MissingError is returned when git cannot be run because it is not
// installed, or not on the PATH of the process running it.
type MissingError struct {
	Err error
}

func (e *MissingError) Error() string {
	return "git is not installed, or not on the PATH of the process running it"
}

func (e *MissingError) Unwrap() error { return e.Err }

// missing returns err as a *MissingError when git was not found.
func missing(err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return &MissingError{Err: err}
	}
	return err
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
		if errors.Is(err, exec.ErrNotFound) {
			log.Error("git could not run", "err", err)
			return missing(err)
		}
		e := &CloneError{URL: url, Output: strings.TrimSpace(stderr.String()), Err: err}
		log.Error("clone failed", "err", err, "output", e.Output, logging.Duration(start))
		return e
	}
	log.Info("cloned", logging.Duration(start))
	return nil
}

// WorktreeError is returned when git could not add a worktree.
type WorktreeError struct {
	Path   string
	Output string // what git said
	Err    error
}

func (e *WorktreeError) Error() string {
	return fmt.Sprintf("git worktree add %s failed: %s", e.Path, e.Output)
}

func (e *WorktreeError) Unwrap() error { return e.Err }

// AddWorktree adds a worktree at path on a new branch, from the HEAD of the
// checkout holding dir.
func AddWorktree(ctx context.Context, dir, path, branch string) error {
	log := logging.FromContext(ctx).With("dir", dir, "path", path, "branch", branch)
	start := time.Now()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "worktree", "add", "-b", branch, "--", path, "HEAD")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			log.Error("git could not run", "err", err)
			return missing(fmt.Errorf("run git: %w", err))
		}
		e := &WorktreeError{Path: path, Output: strings.TrimSpace(stderr.String()), Err: err}
		log.Error("worktree not added", "err", err, "output", e.Output, logging.Duration(start))
		return e
	}
	log.Info("worktree added", logging.Duration(start))
	return nil
}

// WorktreeRemoveError is returned when git would not remove a worktree: it has
// changes, is locked, holds submodules or is a repository's main checkout.
type WorktreeRemoveError struct {
	Path   string
	Output string // what git said
	Err    error
}

func (e *WorktreeRemoveError) Error() string {
	return fmt.Sprintf("git worktree remove %s failed: %s", e.Path, e.Output)
}

func (e *WorktreeRemoveError) Unwrap() error { return e.Err }

// RemoveWorktree removes the worktree at path, or git's entry for it when its
// directory is gone, never by force. dir is in the same repository, and outside the
// worktree.
func RemoveWorktree(ctx context.Context, dir, path string) error {
	log := logging.FromContext(ctx).With("dir", dir, "path", path)
	start := time.Now()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "worktree", "remove", "--", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			log.Error("git could not run", "err", err)
			return missing(fmt.Errorf("run git: %w", err))
		}
		e := &WorktreeRemoveError{Path: path, Output: strings.TrimSpace(stderr.String()), Err: err}
		log.Error("worktree not removed", "err", err, "output", e.Output, logging.Duration(start))
		return e
	}
	log.Info("worktree removed", logging.Duration(start))
	return nil
}

// DeleteBranch deletes a merged branch, and reports false when git refuses: it is
// not merged, or a checkout has it.
func DeleteBranch(ctx context.Context, dir, branch string) (bool, error) {
	return answered(run(ctx, dir, "branch", "-d", "--", branch))
}

// RebaseError is a rebase that did not finish, undone. Conflicts are the files it
// stopped on, none when it failed for another reason.
type RebaseError struct {
	Onto      string
	Conflicts []string
	Output    string // what git said
}

func (e *RebaseError) Error() string {
	if len(e.Conflicts) > 0 {
		return fmt.Sprintf("rebase onto %s stopped on a conflict in %s", e.Onto, strings.Join(e.Conflicts, ", "))
	}
	return fmt.Sprintf("rebase onto %s failed: %s", e.Onto, e.Output)
}

// writeRepo runs a git command that changes the repository, on a request of the user,
// and returns what it said on stderr when it fails.
func writeRepo(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	log := logging.FromContext(ctx).With("dir", dir, "args", args)
	start := time.Now()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(cmd.Environ(), env...)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	if _, ok := errors.AsType[*exec.ExitError](err); err != nil && !ok {
		log.Error("git could not run", "err", err)
		return "", missing(fmt.Errorf("run git: %w", err))
	}
	said := strings.TrimSpace(output.String())
	if err != nil {
		log.Info("git refused", "err", err, "output", said, logging.Duration(start))
		return said, err
	}
	log.Info("git wrote", logging.Duration(start))
	return said, nil
}

// Rebase rebases the checkout at dir onto onto. One that does not finish is aborted,
// leaving all as it was, and is a *RebaseError. It asks for nothing: an editor would
// have nobody to answer it.
func Rebase(ctx context.Context, dir, onto string) error {
	return rebase(ctx, dir, onto, onto)
}

// RebaseFrom moves the commits of the checkout at dir after upstream onto onto, as
// `git rebase --onto`: only its own, whatever became of upstream's. One that does not
// finish is aborted, as Rebase does.
func RebaseFrom(ctx context.Context, dir, onto, upstream string) error {
	return rebase(ctx, dir, onto, "--onto", onto, upstream)
}

func rebase(ctx context.Context, dir, onto string, args ...string) error {
	said, err := writeRepo(ctx, dir, []string{"GIT_EDITOR=true"}, append([]string{"rebase"}, args...)...)
	if err == nil {
		return nil
	}
	e := &RebaseError{Onto: onto, Output: said}
	if files, ferr := run(ctx, dir, "diff", "--name-only", "--diff-filter=U"); ferr == nil && files != "" {
		e.Conflicts = strings.Split(files, "\n")
	}
	if aborted, aerr := writeRepo(ctx, dir, nil, "rebase", "--abort"); aerr != nil {
		logging.FromContext(ctx).Info("no rebase to abort", "dir", dir, "output", aborted)
	}
	return e
}

// FastForward moves the branch of the checkout at dir to commit, with its files. git
// refuses when local changes are in the way, or when it would not be a fast-forward.
func FastForward(ctx context.Context, dir, commit string) error {
	said, err := writeRepo(ctx, dir, nil, "merge", "--ff-only", "--quiet", commit)
	if err != nil {
		return fmt.Errorf("git merge --ff-only: %s", said)
	}
	return nil
}

// MoveBranch moves a branch no checkout has to commit, if that is a fast-forward by
// the value it reads now and it holds that value when it moves.
func MoveBranch(ctx context.Context, dir, branch, commit string) error {
	ref := "refs/heads/" + branch
	old, err := Resolve(ctx, dir, ref)
	if err != nil {
		return err
	}
	ok, err := IsAncestor(ctx, dir, old, commit)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s moved to %s, which %s does not follow", branch, old, commit)
	}
	said, err := writeRepo(ctx, dir, nil, "update-ref", "-m", "wdx: land", ref, commit, old)
	if err != nil {
		return fmt.Errorf("git update-ref: %s", said)
	}
	return nil
}

// Push pushes a branch to origin, asking for no password: nobody is there to type it.
func Push(ctx context.Context, dir, branch string) error {
	said, err := writeRepo(ctx, dir, []string{"GIT_TERMINAL_PROMPT=0"}, "push", "--quiet", "origin", "refs/heads/"+branch+":refs/heads/"+branch)
	if err != nil {
		return fmt.Errorf("git push: %s", said)
	}
	return nil
}

// OriginBranch returns the commit of branch on origin as the repository of dir last
// saw it, its remote-tracking branch; "" when it has none.
func OriginBranch(ctx context.Context, dir, branch string) (string, error) {
	ref := "refs/remotes/origin/" + branch
	ok, err := refExists(ctx, dir, ref)
	if err != nil || !ok {
		return "", err
	}
	return Resolve(ctx, dir, ref)
}

// PushOver pushes a branch rewritten by a rebase to origin, over what is there, but
// only while origin still has it at was: anything pushed there since makes git refuse.
func PushOver(ctx context.Context, dir, branch, was string) error {
	said, err := writeRepo(ctx, dir, []string{"GIT_TERMINAL_PROMPT=0"}, "push", "--quiet",
		"--force-with-lease="+branch+":"+was, "origin", "refs/heads/"+branch+":refs/heads/"+branch)
	if err != nil {
		return fmt.Errorf("git push --force-with-lease: %s", said)
	}
	return nil
}

// BranchExists reports whether the repository holding dir has a local branch
// named branch.
func BranchExists(ctx context.Context, dir, branch string) (bool, error) {
	return answered(run(ctx, dir, "show-ref", "--verify", "--quiet", "refs/heads/"+branch))
}

// HasCommit reports whether the HEAD of the checkout holding dir is a commit:
// false in a repository with none yet.
func HasCommit(ctx context.Context, dir string) (bool, error) {
	return answered(run(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"))
}

// ValidBranch reports whether name can be a branch's name.
func ValidBranch(ctx context.Context, dir, name string) (bool, error) {
	return answered(run(ctx, dir, "check-ref-format", "--branch", name))
}

// Ignored reports whether git ignores path, relative to dir; a trailing slash
// asks about a directory, which need not exist.
func Ignored(ctx context.Context, dir, path string) (bool, error) {
	return answered(run(ctx, dir, "check-ignore", "--quiet", path))
}

// answered is a yes or no git gave by its exit code: 0 or 1 in the commands
// that ask, or 128 from those that reject what they are given.
func answered(_ string, err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		return false, nil
	}
	return false, err
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
		log.Error("git could not run", "err", err, logging.Duration(start))
		return "", missing(fmt.Errorf("run git: %w", err))
	}
	out := strings.TrimSpace(stdout.String())
	log.Info("git ran", "exit", 0, logging.Duration(start))
	log.Debug("git output", "stdout", out)
	return out, nil
}
