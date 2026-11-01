// Package gh is a thin wrapper over GitHub's CLI, for what a pull request says. It
// reaches the network, so it runs only when the user asked for something.
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/logging"
)

// A network that does not answer must not hold up what waits for gh for ever.
const timeout = time.Minute

// ErrMissing is gh not being installed.
var ErrMissing = errors.New("gh is not installed")

// ErrNoPullRequest is a branch with no pull request.
var ErrNoPullRequest = errors.New("no pull request")

// The states of a pull request.
const (
	Open   = "open"
	Merged = "merged"
	Closed = "closed"
)

// PullRequest is what GitHub says of a pull request.
type PullRequest struct {
	Number int
	URL    string
	State  string
	Head   string // the commit it is at
}

// CLI runs gh as Program; with none, as on a machine without it, nothing runs.
type CLI struct {
	Program string
}

// Available reports whether gh can be run.
func (c CLI) Available() bool {
	if c.Program == "" {
		return false
	}
	_, err := exec.LookPath(c.Program)
	return err == nil
}

func (c CLI) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if !c.Available() {
		return nil, ErrMissing
	}
	log := logging.FromContext(ctx).With("dir", dir, "args", args)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(ctx, c.Program, args...)
	cmd.Dir = dir
	// Nobody is there to answer a question, and gh asks some even without a terminal.
	cmd.Env = append(cmd.Environ(), "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0", "NO_COLOR=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		said := strings.TrimSpace(stderr.String())
		log.Info("gh refused", "err", err, "output", said, logging.Duration(start))
		if said == "" {
			return nil, fmt.Errorf("gh: %w", err)
		}
		return nil, errors.New(said)
	}
	log.Info("gh answered", logging.Duration(start))
	return stdout.Bytes(), nil
}

// View returns the latest pull request of branch, in the repository holding dir, or
// ErrNoPullRequest.
func (c CLI) View(ctx context.Context, dir, branch string) (PullRequest, error) {
	out, err := c.run(ctx, dir, "pr", "view", branch, "--json", "number,url,state,headRefOid")
	if err != nil {
		// gh has no exit code of its own for this, only these words.
		if strings.HasPrefix(err.Error(), "no pull requests found") {
			return PullRequest{}, ErrNoPullRequest
		}
		return PullRequest{}, err
	}
	var v struct {
		Number     int    `json:"number"`
		URL        string `json:"url"`
		State      string `json:"state"`
		HeadRefOid string `json:"headRefOid"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return PullRequest{}, fmt.Errorf("read what gh said: %w", err)
	}
	return PullRequest{Number: v.Number, URL: v.URL, State: strings.ToLower(v.State), Head: v.HeadRefOid}, nil
}

var pullURL = regexp.MustCompile(`https?://\S+/pull/(\d+)`)

// Create opens a pull request of branch, already pushed, onto base, with its title
// and text from the commits. It is at the commit head.
func (c CLI) Create(ctx context.Context, dir, base, branch, head string) (PullRequest, error) {
	out, err := c.run(ctx, dir, "pr", "create", "--fill", "--base", base, "--head", branch)
	if err != nil {
		return PullRequest{}, err
	}
	m := pullURL.FindSubmatch(out)
	if m == nil {
		return PullRequest{}, fmt.Errorf("gh said no pull request's address: %s", strings.TrimSpace(string(out)))
	}
	number, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return PullRequest{}, fmt.Errorf("read what gh said: %w", err)
	}
	return PullRequest{Number: number, URL: string(m[0]), State: Open, Head: head}, nil
}
