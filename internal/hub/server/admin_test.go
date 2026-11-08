package server

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/hub/account"
	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/waittest"
)

// tokenOf is the token in the query parameter key of link.
func tokenOf(t *testing.T, link, key string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil || u.Query().Get(key) == "" {
		t.Fatalf("no %s in %q: %v", key, link, err)
	}
	return u.Query().Get(key)
}

// The hub deletes what has expired and logs the head of its audit chain at
// startup and at every interval.
func TestHousekeeping(t *testing.T) {
	t.Parallel()
	var now atomic.Pointer[time.Time]
	began := time.Now()
	now.Store(&began)
	th := start(t, func(o *Options) {
		o.HousekeepingInterval = 10 * time.Millisecond
		o.Now = func() time.Time { return *now.Load() }
	})
	ctx := context.Background()
	seconds := int64(60)
	r, err := th.adminClient(t).CreateInviteWithResponse(ctx, hubapi.CreateInvite{ExpiresSeconds: &seconds})
	if err != nil || r.JSON201 == nil {
		t.Fatalf("invite: %v %s", err, r.Body)
	}
	head, err := audit.Head(ctx, th.server.db)
	if err != nil || head.Rows != 1 {
		t.Fatalf("want one row, got %+v, %v", head, err)
	}
	// Two lines with the invite's row: logged at an interval, not once.
	var seen int
	headLogged := func() bool {
		seen = 0
		for _, line := range logLines(t, th.home) {
			if line["msg"] == "audit chain head" && line["rows"] == float64(1) && line["head"] == head.Head {
				seen++
			}
		}
		return seen >= 2
	}
	if !waittest.Until(t, headLogged) {
		t.Fatalf("want the head logged twice, got %d", seen)
	}
	later := began.Add(time.Hour)
	now.Store(&later)
	deleted := func() bool {
		for _, line := range logLines(t, th.home) {
			if line["msg"] == "expired deleted" && line["invites"] == float64(1) {
				return true
			}
		}
		return false
	}
	if !waittest.Until(t, deleted) {
		t.Fatal("want the expired invite deleted")
	}
}

// An invite's link and a reset's are measured in the log, never logged, nor
// is their token.
func TestLinksNeverLogged(t *testing.T) {
	t.Parallel()
	th := start(t, nil)
	ctx := context.Background()
	admin := th.adminClient(t)
	inv, err := admin.CreateInviteWithResponse(ctx, hubapi.CreateInvite{})
	if err != nil || inv.JSON201 == nil {
		t.Fatalf("invite: %v %s", err, inv.Body)
	}
	link := inv.JSON201.Invite.Link
	if !strings.HasPrefix(link, "http://localhost:8080/signup?invite=") {
		t.Fatalf("unexpected link %q", link)
	}
	if got := time.Until(inv.JSON201.Invite.ExpiresAt); got < 23*time.Hour || got > DefaultInviteLife {
		t.Fatalf("want 24 hours by default, got %s", got)
	}
	_, _, err = th.server.Accounts().SignUp(ctx, account.By{Actor: audit.ActorUser},
		account.SignUp{Invite: tokenOf(t, link, "invite"), Username: "alice", Password: "a password long enough"})
	if err != nil {
		t.Fatal(err)
	}
	second := true
	reset, err := admin.CreateUserResetWithResponse(ctx, "ALICE", hubapi.CreateReset{SecondFactor: &second})
	if err != nil || reset.JSON201 == nil || !reset.JSON201.Reset.SecondFactor {
		t.Fatalf("reset: %v %s", err, reset.Body)
	}
	resetLink := reset.JSON201.Reset.Link
	if !strings.HasPrefix(resetLink, "http://localhost:8080/reset?token=") {
		t.Fatalf("unexpected link %q", resetLink)
	}
	if err := th.stop(); err != nil {
		t.Fatal(err)
	}
	measured := 0
	for _, line := range logLines(t, th.home) {
		if line["msg"] != "request returned" || line["status"] != float64(201) {
			continue
		}
		resp, _ := line["response"].(map[string]any)
		for _, key := range []string{"invite", "reset"} {
			if m, ok := resp[key].(map[string]any); ok && m["link_bytes"] != nil && m["link"] == nil {
				measured++
			}
		}
	}
	if measured != 2 {
		t.Fatalf("want both links measured, got %d", measured)
	}
	files, err := filepath.Glob(filepath.Join(hubconfig.LogsDir(th.home), "*.log"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no logs: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{tokenOf(t, link, "invite"), tokenOf(t, resetLink, "token")} {
			if strings.Contains(string(b), secret) {
				t.Fatalf("%s holds a token", f)
			}
		}
	}
}

func TestAuditBroken(t *testing.T) {
	t.Parallel()
	th := start(t, nil)
	ctx := context.Background()
	admin := th.adminClient(t)
	for range 3 {
		if r, err := admin.CreateInviteWithResponse(ctx, hubapi.CreateInvite{}); err != nil || r.JSON201 == nil {
			t.Fatalf("invite: %v", err)
		}
	}
	v, err := admin.VerifyAuditWithResponse(ctx)
	if err != nil || v.JSON200 == nil || v.JSON200.Audit.Rows != 3 || v.JSON200.Audit.Head == nil {
		t.Fatalf("want 3 rows verified, got %v %s", err, v.Body)
	}
	if _, err := th.server.db.ExecContext(ctx, "UPDATE audit SET action = 'user.removed' WHERE id = 2"); err != nil {
		t.Fatal(err)
	}
	v, err = admin.VerifyAuditWithResponse(ctx)
	if err != nil || v.ApplicationproblemJSONDefault == nil {
		t.Fatalf("want a problem, got %v %s", err, v.Body)
	}
	p := v.ApplicationproblemJSONDefault
	if p.Code != hubapi.ErrorCodeAuditBroken || p.Details == nil || (*p.Details)["id"] != float64(2) || (*p.Details)["at"] == nil {
		t.Fatalf("want row 2 broken, got %+v", p)
	}
}
