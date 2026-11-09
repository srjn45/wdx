package aca

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/srjn45/wdx/internal/atomicfile"
)

// UsageFetcher is an AI coding agent whose account's usage is told by its vendor's
// API only, not by anything its CLI writes: FetchUsage asks for it and keeps the
// answer in l.BackupDir, where Usage reads it. The daemon calls it while the agent
// works, at most once an interval. Only Cursor is one (the wdx doc USAGE_CURSOR).
type UsageFetcher interface {
	FetchUsage(ctx context.Context, l Launch, client *http.Client) error
}

// ErrUsageDenied is what FetchUsage answers when the vendor refused the login it
// was given: logged out, or a token expired that the CLI has not refreshed yet.
var ErrUsageDenied = errors.New("usage refused: not logged in")

// UsageFetchError is a fetch the vendor answered with other than success: its HTTP
// status, and its error code when it gave one. The body is never kept: it may say
// who the user is.
type UsageFetchError struct {
	Status int
	Code   string // the Connect error code, as unauthenticated; "" when none
}

func (e *UsageFetchError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("usage fetch answered %d", e.Status)
	}
	return fmt.Sprintf("usage fetch answered %d, %s", e.Status, e.Code)
}

// Is makes a 401 ErrUsageDenied.
func (e *UsageFetchError) Is(target error) bool {
	return target == ErrUsageDenied && e.Status == http.StatusUnauthorized
}

const (
	// DefaultCursorEndpoint is Cursor's API, as its CLI's --endpoint defaults to.
	DefaultCursorEndpoint = "https://api2.cursor.sh"
	// cursorUsagePath is the call Cursor's /usage screen makes for its numbers.
	cursorUsagePath = "/aiserver.v1.DashboardService/GetCurrentPeriodUsage"
	// cursorUsageFile is, in the backup, the last answer to that call, whole.
	cursorUsageFile = "cursor-usage.json"
	// cursorUsageLimit bounds the answer read: the one seen is 3 KB.
	cursorUsageLimit = 1 << 20
)

// cursorPeriodUsage is what Usage reads of GetCurrentPeriodUsage's answer. The
// percentages are in percent, as 0.0889 for 0.0889 %; one the answer leaves out is
// not told. Checked on 2026.11.05 on a Pro plan.
type cursorPeriodUsage struct {
	BillingCycleEnd int64 `json:"billingCycleEnd,string"` // unix ms
	PlanUsage       *struct {
		TotalPercentUsed *float64 `json:"totalPercentUsed"` // the included allowance, what /usage shows as Included
		AutoPercentUsed  *float64 `json:"autoPercentUsed"`  // the Auto bucket
		APIPercentUsed   *float64 `json:"apiPercentUsed"`   // the API bucket, the models named
	} `json:"planUsage"`
}

// Usage implements AiCodingAgent, from the answer FetchUsage last kept: the billing
// month, as the window month, of the included allowance every model draws on, of the
// Auto bucket and of the API bucket, each rounded to a whole percent and reset at
// the cycle's end, observed when the answer was written. On-demand spend is left
// out. No answer kept, or one without the plan's usage, as an Enterprise plan's, is
// ErrNoUsage.
func (Cursor) Usage(l Launch) ([]Usage, []string, error) {
	if l.BackupDir == "" {
		return nil, nil, ErrNoUsage
	}
	path := filepath.Join(l.BackupDir, cursorUsageFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, ErrNoUsage
	}
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	var answer cursorPeriodUsage
	if err := json.Unmarshal(data, &answer); err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", cursorUsageFile, err)
	}
	if answer.PlanUsage == nil {
		return nil, nil, ErrNoUsage
	}
	var resets time.Time
	if answer.BillingCycleEnd != 0 {
		resets = time.UnixMilli(answer.BillingCycleEnd)
	}
	var rows []Usage
	for _, b := range []struct {
		name    string
		percent *float64
	}{{"", answer.PlanUsage.TotalPercentUsed}, {"auto", answer.PlanUsage.AutoPercentUsed}, {"api", answer.PlanUsage.APIPercentUsed}} {
		if b.percent == nil {
			continue
		}
		rows = append(rows, Usage{Window: "month", Bucket: b.name, UsedPercent: int(math.Round(*b.percent)), ResetsAt: resets, ObservedAt: info.ModTime()})
	}
	if len(rows) == 0 {
		return nil, nil, ErrNoUsage
	}
	return rows, nil, nil
}

// cursorAuthFile is where Cursor's CLI keeps its login on Linux, under the home:
// $XDG_CONFIG_HOME/cursor/auth.json, which the agents' terminals do not inherit.
var cursorAuthFile = filepath.Join(".config", "cursor", "auth.json")

// FetchUsage implements UsageFetcher: it asks Cursor's API for the current billing
// period's usage, as Cursor's /usage screen does, with the access token Cursor's CLI
// keeps, read anew each time and never kept, logged or refreshed by wdx, and writes
// the answer whole to the backup. No login file, or one without a token, is
// ErrNoUsage and asks nothing; a refused login is ErrUsageDenied and writes nothing.
func (c Cursor) FetchUsage(ctx context.Context, l Launch, client *http.Client) error {
	if l.BackupDir == "" {
		return ErrNoUsage
	}
	token, err := c.accessToken(l)
	if err != nil {
		return err
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = DefaultCursorEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+cursorUsagePath, bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, cursorUsageLimit))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var connect struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(body, &connect) // a body that is no Connect error has no code
		return &UsageFetchError{Status: resp.StatusCode, Code: connect.Code}
	}
	if !json.Valid(body) {
		return errors.New("usage fetch answered what is not JSON")
	}
	return atomicfile.Write(filepath.Join(l.BackupDir, cursorUsageFile), body, 0o600)
}

// accessToken is the access token in Cursor's login file, at c.AuthFile, or under
// l.UserHome when that is "".
func (c Cursor) accessToken(l Launch) (string, error) {
	path := c.AuthFile
	if path == "" {
		if l.UserHome == "" {
			return "", ErrNoUsage
		}
		path = filepath.Join(l.UserHome, cursorAuthFile)
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNoUsage
	}
	if err != nil {
		return "", fmt.Errorf("%w: Cursor's login not read: %w", ErrNoUsage, err)
	}
	var auth struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(data, &auth); err != nil {
		// The error of a file that does not parse may quote it.
		return "", fmt.Errorf("%w: Cursor's login %s is not JSON", ErrNoUsage, path)
	}
	if auth.AccessToken == "" {
		return "", ErrNoUsage
	}
	return auth.AccessToken, nil
}

// cursorModelBuckets is the families of `cursor-agent models`: Auto draws on auto
// besides the included allowance, every other model on that allowance alone; api is
// API-key spend, no model's.
var cursorModelBuckets = []modelFamily{
	{ids: []string{"auto"}, names: []string{"Auto"}, buckets: []string{"auto"}},
	{
		idPrefixes: []string{"gpt-", "claude-", "gemini-", "grok-", "cursor-", "composer-", "muse-", "kimi-", "glm-", "deepseek-"},
		nameWords:  []string{"GPT", "Codex", "Claude", "Gemini", "Grok", "Composer", "Muse", "Kimi", "GLM", "DeepSeek"},
	},
}

// ModelBuckets implements AiCodingAgent, from cursorModelBuckets.
func (Cursor) ModelBuckets(id, name string) ([]string, bool) {
	return modelBuckets(cursorModelBuckets, id, name)
}
