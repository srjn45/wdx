package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/logging"
)

// The reads below may run beside any git command of a human or an agent, so they
// never write to the repository (docs/TIDE_WORKTREES.md, "The lock rule"). Measured
// on a stale index: GIT_OPTIONAL_LOCKS=0 alone leaves `git diff HEAD` rewriting
// the index, under index.lock, and so failing a git add or pull running then.
var (
	readEnv = []string{
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_LITERAL_PATHSPECS=1", // a path is a path, whatever a client sends
	}
	readConfig = []string{
		"-c", "diff.autoRefreshIndex=false",
		"-c", "core.fsmonitor=false", // a read never starts a daemon
		"-c", "core.quotepath=off", // a path is shown as it is
	}
)

// maxPatch is the most of a diff a read keeps; the rest is dropped, and said to be.
const maxPatch = 8 << 20

// wholeFile is a context long enough to show any file whole.
const wholeFile = "--unified=1000000000"

// fetching says whether a read may download what a partial clone lacks. Only one
// a user asked for may: one that runs by itself must not reach the network.
type fetching bool

const (
	noDownload    fetching = false
	mayDownload   fetching = true
	noLazyFetchOn          = "GIT_NO_LAZY_FETCH=1"
)

// ReadError is git refusing a read: the answer to some, the failure of others.
type ReadError struct {
	Args   []string
	Exit   int
	Stderr string
}

func (e *ReadError) Error() string {
	return fmt.Sprintf("git %s: exit %d: %s", strings.Join(e.Args, " "), e.Exit, e.Stderr)
}

// read runs a git command that leaves the repository as it found it. Its stdout
// is kept up to maxPatch; the reader of the rest goes on, so that git ends by
// itself, and truncated says so.
func read(ctx context.Context, dir string, fetch fetching, args ...string) (out []byte, truncated bool, err error) {
	log := logging.FromContext(ctx).With("dir", dir, "args", args)
	start := time.Now()
	cmd := exec.CommandContext(ctx, "git", append(append([]string{"-C", dir}, readConfig...), args...)...)
	cmd.Env = append(cmd.Environ(), readEnv...)
	if fetch == noDownload {
		cmd.Env = append(cmd.Env, noLazyFetchOn)
	}
	stdout := &boundedBuffer{room: maxPatch}
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = stdout, &stderr
	err = cmd.Run()
	if e, ok := errors.AsType[*exec.ExitError](err); ok {
		msg := strings.TrimSpace(stderr.String())
		log.Debug("git read", "exit", e.ExitCode(), "stderr", msg, logging.Duration(start))
		return stdout.Bytes(), stdout.dropped, &ReadError{Args: args, Exit: e.ExitCode(), Stderr: msg}
	}
	if err != nil {
		log.Error("git could not run", "err", err, logging.Duration(start))
		return nil, false, missing(fmt.Errorf("run git: %w", err))
	}
	log.Debug("git read", "exit", 0, logging.Duration(start))
	return stdout.Bytes(), stdout.dropped, nil
}

// boundedBuffer keeps what fits in room and drops the rest.
type boundedBuffer struct {
	bytes.Buffer
	room    int
	dropped bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n > b.room {
		p = p[:b.room]
		b.dropped = true
	}
	b.room -= len(p)
	b.Buffer.Write(p)
	return n, nil
}

var _ io.Writer = (*boundedBuffer)(nil)

// readText is read's output as trimmed text, for the answers that fit a line.
func readText(ctx context.Context, dir string, args ...string) (string, error) {
	out, _, err := read(ctx, dir, noDownload, args...)
	return strings.TrimSpace(string(out)), err
}

// isAnswer reports whether err is git saying no rather than failing to run.
func isAnswer(err error) bool {
	_, ok := errors.AsType[*ReadError](err)
	return ok
}

// Checkout is an entry of `git worktree list`.
type Checkout struct {
	Path     string
	Head     string // "" with no commit yet
	Branch   string // "" when HEAD is detached
	Prunable bool   // its directory is gone
}

// Checkouts lists the checkouts of the repository holding dir, the main one
// first. A bare repository's own entry has no files, so it is left out.
func Checkouts(ctx context.Context, dir string) ([]Checkout, error) {
	out, err := readText(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var list []Checkout
	for block := range strings.SplitSeq(out, "\n\n") {
		var c Checkout
		bare := false
		for line := range strings.SplitSeq(block, "\n") {
			key, value, _ := strings.Cut(line, " ")
			switch key {
			case "worktree":
				c.Path = value
			case "HEAD":
				c.Head = strings.TrimLeft(value, "0") // all zeros: no commit yet
				if c.Head != "" {
					c.Head = value
				}
			case "branch":
				c.Branch = strings.TrimPrefix(value, "refs/heads/")
			case "bare":
				bare = true
			case "prunable":
				c.Prunable = true
			}
		}
		if c.Path != "" && !bare {
			list = append(list, c)
		}
	}
	return list, nil
}

// Dirs returns the git directory of the checkout at dir, and the one all
// checkouts of its repository share.
func Dirs(ctx context.Context, dir string) (gitDir, common string, err error) {
	out, err := readText(ctx, dir, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	if err != nil {
		return "", "", err
	}
	gitDir, common, ok := strings.Cut(out, "\n")
	if !ok {
		return "", "", fmt.Errorf("git rev-parse: unexpected output %q", out)
	}
	return gitDir, common, nil
}

// FileChange is a file with uncommitted changes.
type FileChange struct {
	Path string
	// Status is one letter: M changed, A added, D deleted, ? untracked, U in
	// conflict.
	Status string
}

// Changes lists the files of the checkout at dir with uncommitted changes:
// staged, not staged and untracked, by path. A rename is its delete and its add,
// each with a diff of its own.
func Changes(ctx context.Context, dir string) ([]FileChange, error) {
	out, _, err := read(ctx, dir, noDownload, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	if err != nil {
		return nil, err
	}
	var list []FileChange
	for entry := range strings.SplitSeq(string(out), "\x00") {
		if len(entry) < 4 {
			continue
		}
		list = append(list, FileChange{Path: entry[3:], Status: changeLetter(entry[:2])})
	}
	return list, nil
}

// changeLetter is the one letter for status's two, for the index and the files.
func changeLetter(xy string) string {
	switch {
	case xy == "??":
		return "?"
	case strings.Contains(xy, "U"), xy == "AA", xy == "DD":
		return "U"
	case strings.Contains(xy, "D"):
		return "D"
	case xy[0] == 'A':
		return "A"
	}
	return "M"
}

// DefaultBranch returns the name of the repository's default branch: the one
// origin/HEAD names, else main, else master; "" when it has none of them.
func DefaultBranch(ctx context.Context, dir string) (string, error) {
	out, err := readText(ctx, dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	switch {
	case err == nil:
		return strings.TrimPrefix(out, "origin/"), nil
	case !isAnswer(err):
		return "", err
	}
	for _, name := range []string{"main", "master"} {
		ok, err := refExists(ctx, dir, "refs/heads/"+name)
		if err != nil {
			return "", err
		}
		if ok {
			return name, nil
		}
	}
	return "", nil
}

func refExists(ctx context.Context, dir, ref string) (bool, error) {
	_, err := readText(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil && isAnswer(err) {
		return false, nil
	}
	return err == nil, err
}

// BaseOf returns what the checkout at dir on branch is compared with: the local
// default branch, or the same on origin when it is on that branch itself, so that
// it shows what is not pushed. "" when there is neither.
func BaseOf(ctx context.Context, dir, branch string) (string, error) {
	name, err := DefaultBranch(ctx, dir)
	if err != nil || name == "" {
		return "", err
	}
	if branch != name {
		ok, err := refExists(ctx, dir, "refs/heads/"+name)
		if err != nil {
			return "", err
		}
		if ok {
			return name, nil
		}
	}
	ok, err := refExists(ctx, dir, "refs/remotes/origin/"+name)
	if err != nil || !ok {
		return "", err
	}
	return "origin/" + name, nil
}

// Summary is how the commits of a checkout differ from its base.
type Summary struct {
	Ahead int
	// Lines says whether Added and Removed are known: in a partial clone they may
	// take files not downloaded, and a read that runs by itself downloads nothing.
	Lines          bool
	Added, Removed int
}

// Summarize returns the summary of the checkout at dir against base, from their
// merge base. It fails when the two have no history in common.
func Summarize(ctx context.Context, dir, base string) (Summary, error) {
	out, err := readText(ctx, dir, "rev-list", "--count", base+"..HEAD")
	if err != nil {
		return Summary{}, err
	}
	ahead, err := strconv.Atoi(out)
	if err != nil {
		return Summary{}, fmt.Errorf("git rev-list: unexpected output %q", out)
	}
	s := Summary{Ahead: ahead}
	stat, _, err := read(ctx, dir, noDownload, "diff", "--numstat", "--no-renames", "--no-ext-diff", base+"...HEAD")
	if err != nil {
		if isAnswer(err) {
			logging.FromContext(ctx).Info("lines of a worktree not known", "dir", dir, "err", err)
			return s, nil
		}
		return Summary{}, err
	}
	s.Lines = true
	for line := range strings.SplitSeq(string(stat), "\n") {
		added, rest, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		removed, _, _ := strings.Cut(rest, "\t")
		// A binary file counts "-".
		a, _ := strconv.Atoi(added)
		r, _ := strconv.Atoi(removed)
		s.Added += a
		s.Removed += r
	}
	return s, nil
}

// Patch is the text of a diff, "" when nothing differs.
type Patch struct {
	Text      string
	Binary    bool // git says only that a binary file differs
	Truncated bool // too long: Text is its beginning
}

var binaryPatch = regexp.MustCompile(`(?m)^(Binary files .* differ|GIT binary patch)$`)

func patchOf(out []byte, truncated bool) Patch {
	return Patch{Text: string(out), Binary: binaryPatch.Match(out), Truncated: truncated}
}

// diffFlags ask for full ids on a patch's index lines: what was on the screen is
// what a file's verified mark holds.
var diffFlags = []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames", "--full-index"}

// FileDiff returns the uncommitted changes of the file at path, relative to the
// checkout at dir, whole: every line of the file is in the patch. A user asked
// for it, so what a partial clone lacks is downloaded.
func FileDiff(ctx context.Context, dir, path string) (Patch, error) {
	head, err := headTree(ctx, dir)
	if err != nil {
		return Patch{}, err
	}
	out, truncated, err := read(ctx, dir, mayDownload, append(diffFlags, wholeFile, head, "--", path)...)
	if err != nil {
		return Patch{}, err
	}
	if len(out) > 0 {
		return patchOf(out, truncated), nil
	}
	status, _, err := read(ctx, dir, noDownload, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--", path)
	if err != nil {
		return Patch{}, err
	}
	if !bytes.HasPrefix(status, []byte("?? ")) {
		return Patch{}, nil
	}
	return untrackedDiff(ctx, dir, path, wholeFile)
}

// untrackedDiff is an untracked file compared with nothing, all added: git diff
// never shows one.
func untrackedDiff(ctx context.Context, dir, path string, flags ...string) (Patch, error) {
	args := append(append([]string{"diff", "--no-index", "--no-color", "--no-ext-diff", "--full-index"}, flags...), "--", "/dev/null", path)
	out, truncated, err := read(ctx, dir, noDownload, args...)
	if e, ok := errors.AsType[*ReadError](err); ok && e.Exit == 1 {
		err = nil // differing is what --no-index reports with it
	}
	if err != nil {
		return Patch{}, err
	}
	return patchOf(out, truncated), nil
}

// UncommittedDiff returns everything not committed in the checkout at dir, staged,
// not staged and untracked, as one patch of the changes only. A user asked for it,
// so what a partial clone lacks is downloaded.
func UncommittedDiff(ctx context.Context, dir string) (Patch, error) {
	head, err := headTree(ctx, dir)
	if err != nil {
		return Patch{}, err
	}
	out, truncated, err := read(ctx, dir, mayDownload, append(diffFlags, head)...)
	if err != nil {
		return Patch{}, err
	}
	all := patchOf(out, truncated)
	changes, err := Changes(ctx, dir)
	if err != nil {
		return Patch{}, err
	}
	for _, c := range changes {
		if c.Status != "?" {
			continue
		}
		if all.Truncated || len(all.Text) >= maxPatch {
			all.Truncated = true
			break
		}
		p, err := untrackedDiff(ctx, dir, c.Path)
		if err != nil {
			return Patch{}, err
		}
		all.Text += p.Text
		all.Binary = all.Binary || p.Binary
		all.Truncated = p.Truncated
	}
	return all, nil
}

// headTree is what uncommitted changes are measured from: HEAD or, before the
// first commit, the empty tree.
func headTree(ctx context.Context, dir string) (string, error) {
	_, err := readText(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	switch {
	case err == nil:
		return "HEAD", nil
	case !isAnswer(err):
		return "", err
	}
	return readText(ctx, dir, "hash-object", "-t", "tree", "--stdin", "--no-filters")
}

// ContentID returns the id git would store the file at path in the checkout at dir
// with, which it does not store: the same before and after it is committed.
func ContentID(ctx context.Context, dir, path string) (string, error) {
	return readText(ctx, dir, "hash-object", "--", path)
}

// CommittedFile is a file a checkout's commits changed.
type CommittedFile struct {
	Path string
	// Status is one letter: M changed, A added, D deleted.
	Status string
}

// CommittedFiles lists the files the checkout at dir changed since it left base. It
// reads trees alone, so it downloads nothing a partial clone lacks.
func CommittedFiles(ctx context.Context, dir, base string) ([]CommittedFile, error) {
	out, _, err := read(ctx, dir, noDownload, "diff", "--name-status", "-z", "--no-renames", "--no-ext-diff", base+"...HEAD")
	if err != nil {
		return nil, err
	}
	fields := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	var list []CommittedFile
	for i := 0; i+1 < len(fields); i += 2 {
		status := "M"
		if fields[i] == "A" || fields[i] == "D" {
			status = fields[i]
		}
		list = append(list, CommittedFile{Path: fields[i+1], Status: status})
	}
	return list, nil
}

// Resolve returns the commit ref names in the repository of dir.
func Resolve(ctx context.Context, dir, ref string) (string, error) {
	return readText(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
}

// IsAncestor reports whether every commit of ancestor is in commit: a fast-forward
// from one to the other is possible.
func IsAncestor(ctx context.Context, dir, ancestor, commit string) (bool, error) {
	_, err := readText(ctx, dir, "merge-base", "--is-ancestor", ancestor, commit)
	if err == nil {
		return true, nil
	}
	if isAnswer(err) {
		return false, nil
	}
	return false, err
}

// OperationInProgress reports whether the checkout at dir is in the middle of
// something git can be told to go on with, which HEAD and the files say nothing of.
func OperationInProgress(ctx context.Context, dir string) (bool, error) {
	gitDir, _, err := Dirs(ctx, dir)
	if err != nil {
		return false, err
	}
	for _, name := range []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG"} {
		if _, err := os.Stat(filepath.Join(gitDir, name)); err == nil {
			return true, nil
		}
	}
	return false, nil
}

// MergeBase returns where the checkout at dir left base, "" when they have no
// history in common.
func MergeBase(ctx context.Context, dir, base string) (string, error) {
	out, err := readText(ctx, dir, "merge-base", base, "HEAD")
	if err != nil && isAnswer(err) {
		return "", nil
	}
	return out, err
}

// RangeDiff returns what is committed in the checkout at dir since it left base,
// as a PR would show it. A user asked for it, so what a partial clone lacks is
// downloaded.
func RangeDiff(ctx context.Context, dir, base string) (Patch, error) {
	out, truncated, err := read(ctx, dir, mayDownload, append(diffFlags, base+"...HEAD")...)
	if err != nil {
		return Patch{}, err
	}
	return patchOf(out, truncated), nil
}
