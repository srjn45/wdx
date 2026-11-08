package aca_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
)

// cursorUsage puts the fixture name in backup as the answer the fetch keeps, and
// returns its modification time.
func cursorUsage(t *testing.T, backup, name string) time.Time {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "cursor", "2026.11.05", "usage", name))
	if err != nil {
		t.Fatal(err)
	}
	return writeCursorUsage(t, backup, data)
}

func writeCursorUsage(t *testing.T, backup string, data []byte) time.Time {
	t.Helper()
	path := filepath.Join(backup, "cursor-usage.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime()
}

func usageRows(rows []aca.Usage) string {
	var text string
	for _, r := range rows {
		text += r.Window + "/" + r.Bucket + " " + strconv.Itoa(r.UsedPercent) + " " + r.ResetsAt.UTC().Format(time.RFC3339) + "; "
	}
	return text
}

// TestCursorUsage reads GetCurrentPeriodUsage's answer on a Pro plan, before and after
// a prompt: the billing month of the included allowance, of Auto and of API, rounded
// as Cursor's numbers are tiny there, reset at the cycle's end, observed when kept.
func TestCursorUsage(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"current-period-usage.json", "current-period-usage-after-prompt.json"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l := aca.Launch{BackupDir: t.TempDir()}
			observed := cursorUsage(t, l.BackupDir, name)
			got, follow, err := aca.Cursor{}.Usage(l)
			if err != nil || follow != nil {
				t.Fatalf("usage = %+v, %v, following %q", got, err, follow)
			}
			const want = "month/ 0 2026-12-01T18:31:27Z; month/auto 0 2026-12-01T18:31:27Z; month/api 0 2026-12-01T18:31:27Z; "
			if usageRows(got) != want {
				t.Errorf("rows = %s, want %s", usageRows(got), want)
			}
			for _, r := range got {
				if !r.ObservedAt.Equal(observed) {
					t.Errorf("%s observed at %v, want %v", r.Bucket, r.ObservedAt, observed)
				}
			}
		})
	}
}

// TestCursorUsageRounds: each percentage is rounded to the nearest whole one, and one
// the answer leaves out is no row.
func TestCursorUsageRounds(t *testing.T) {
	t.Parallel()
	l := aca.Launch{BackupDir: t.TempDir()}
	writeCursorUsage(t, l.BackupDir, []byte(`{"billingCycleEnd": "1796149887000", "planUsage": {"totalPercentUsed": 62.5, "autoPercentUsed": 100.2}}`))
	got, _, err := aca.Cursor{}.Usage(l)
	if want := "month/ 63 2026-12-01T18:31:27Z; month/auto 100 2026-12-01T18:31:27Z; "; err != nil || usageRows(got) != want {
		t.Errorf("usage = %s, %v; want %s", usageRows(got), err, want)
	}
}

// TestCursorUsageUnknown: nothing is told before a fetch kept an answer, by an answer
// without the plan's usage (an Enterprise plan), by a Connect error, or with no
// backup; an answer that does not parse is an error to log.
func TestCursorUsageUnknown(t *testing.T) {
	t.Parallel()
	l := aca.Launch{BackupDir: t.TempDir()}
	if _, _, err := (aca.Cursor{}).Usage(l); !errors.Is(err, aca.ErrNoUsage) {
		t.Errorf("before a fetch: %v", err)
	}
	for _, name := range []string{"no-plan-usage.json", "unauthenticated.json"} {
		cursorUsage(t, l.BackupDir, name)
		if _, _, err := (aca.Cursor{}).Usage(l); !errors.Is(err, aca.ErrNoUsage) {
			t.Errorf("%s: %v", name, err)
		}
	}
	writeCursorUsage(t, l.BackupDir, []byte(`{"planUsage": {}}`))
	if _, _, err := (aca.Cursor{}).Usage(l); !errors.Is(err, aca.ErrNoUsage) {
		t.Errorf("no percentage: %v", err)
	}
	writeCursorUsage(t, l.BackupDir, []byte(`{"planUsage": {`))
	if _, _, err := (aca.Cursor{}).Usage(l); err == nil || errors.Is(err, aca.ErrNoUsage) {
		t.Errorf("an answer that does not parse: %v", err)
	}
	if _, _, err := (aca.Cursor{}).Usage(aca.Launch{}); !errors.Is(err, aca.ErrNoUsage) {
		t.Errorf("no backup: %v", err)
	}
}

// cursorAPI stands in for Cursor's API: it answers GetCurrentPeriodUsage with status
// and the fixture name, after checking the request is the one Cursor's CLI makes.
func cursorAPI(t *testing.T, status int, name string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	answer, err := os.ReadFile(filepath.Join("testdata", "cursor", "2026.11.05", "usage", name))
	if err != nil {
		t.Fatal(err)
	}
	calls := new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.URL.Path != "/aiserver.v1.DashboardService/GetCurrentPeriodUsage" || string(body) != "{}" ||
			r.Header.Get("Authorization") != "Bearer the-token" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Connect-Protocol-Version") != "1" {
			t.Errorf("request %s %s %q, headers %v", r.Method, r.URL.Path, body, r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(answer)
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

// cursorLogin writes a login file as Cursor's CLI keeps it, and returns its path.
func cursorLogin(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"accessToken": "the-token", "refreshToken": "the-refresh"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCursorFetchUsage: the answer is kept whole in the backup, where Usage reads it.
func TestCursorFetchUsage(t *testing.T) {
	t.Parallel()
	srv, calls := cursorAPI(t, http.StatusOK, "current-period-usage-after-prompt.json")
	c := aca.Cursor{Endpoint: srv.URL, AuthFile: cursorLogin(t)}
	l := aca.Launch{BackupDir: t.TempDir()}
	if err := c.FetchUsage(context.Background(), l, srv.Client()); err != nil || calls.Load() != 1 {
		t.Fatalf("fetch: %v, %d calls", err, calls.Load())
	}
	got, _, err := c.Usage(l)
	if err != nil || len(got) != 3 || !got[0].ResetsAt.Equal(time.UnixMilli(1796149887000)) {
		t.Errorf("usage = %s, %v", usageRows(got), err)
	}
}

// TestCursorFetchUsageDenied: a refused login writes nothing, keeps the last answer,
// and says so with the status and Connect code, never the body.
func TestCursorFetchUsageDenied(t *testing.T) {
	t.Parallel()
	srv, _ := cursorAPI(t, http.StatusUnauthorized, "unauthenticated.json")
	c := aca.Cursor{Endpoint: srv.URL, AuthFile: cursorLogin(t)}
	l := aca.Launch{BackupDir: t.TempDir()}
	err := c.FetchUsage(context.Background(), l, srv.Client())
	var fetch *aca.UsageFetchError
	if !errors.Is(err, aca.ErrUsageDenied) || !errors.As(err, &fetch) || fetch.Status != 401 || fetch.Code != "unauthenticated" {
		t.Fatalf("fetch: %v", err)
	}
	if _, _, err := c.Usage(l); !errors.Is(err, aca.ErrNoUsage) {
		t.Errorf("after a refused fetch: %v", err)
	}
}

// TestCursorFetchUsageNoLogin: with no login file, or one with no token, nothing is
// asked.
func TestCursorFetchUsageNoLogin(t *testing.T) {
	t.Parallel()
	srv, calls := cursorAPI(t, http.StatusOK, "current-period-usage.json")
	empty := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(empty, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []aca.Cursor{
		{Endpoint: srv.URL, AuthFile: filepath.Join(t.TempDir(), "auth.json")},
		{Endpoint: srv.URL, AuthFile: empty},
		{Endpoint: srv.URL}, // and no home to look in
	} {
		if err := c.FetchUsage(context.Background(), aca.Launch{BackupDir: t.TempDir()}, srv.Client()); !errors.Is(err, aca.ErrNoUsage) {
			t.Errorf("%s: %v", c.AuthFile, err)
		}
	}
	home := t.TempDir()
	if err := (aca.Cursor{Endpoint: srv.URL}).FetchUsage(context.Background(), aca.Launch{BackupDir: t.TempDir(), UserHome: home}, srv.Client()); !errors.Is(err, aca.ErrNoUsage) {
		t.Errorf("a home with no login: %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("asked %d times with no login", calls.Load())
	}
}

// TestCursorFetchUsageFromTheHome: the login is read from ~/.config/cursor/auth.json
// in the user's home.
func TestCursorFetchUsageFromTheHome(t *testing.T) {
	t.Parallel()
	srv, calls := cursorAPI(t, http.StatusOK, "current-period-usage.json")
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "cursor")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"accessToken": "the-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (aca.Cursor{Endpoint: srv.URL}).FetchUsage(context.Background(), aca.Launch{BackupDir: t.TempDir(), UserHome: home}, srv.Client()); err != nil || calls.Load() != 1 {
		t.Errorf("fetch: %v, %d calls", err, calls.Load())
	}
}
