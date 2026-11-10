package pages_test

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/hub/account"
	"github.com/srjn45/wdx/internal/hub/server"
)

func TestCrossSiteRefused(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	b := h.browser()
	form := b.get("/login")
	fill := map[string]string{"username": "alice", "password": "wrong password!"}
	for _, tc := range []struct {
		header, value string
		status        int
	}{
		{"Sec-Fetch-Site", "cross-site", http.StatusForbidden},
		{"Sec-Fetch-Site", "same-site", http.StatusForbidden},
		{"Origin", "http://evil.example", http.StatusForbidden},
		{"Sec-Fetch-Site", "same-origin", http.StatusUnprocessableEntity},
		// public_url's origin is trusted, though the test reaches the hub
		// at another.
		{"Origin", "http://localhost:8080", http.StatusUnprocessableEntity},
	} {
		b.header = http.Header{}
		b.header.Set(tc.header, tc.value)
		if got := b.submit(form, "/login", fill); got.status != tc.status {
			t.Errorf("%s: %s: got %d, want %d:\n%s", tc.header, tc.value, got.status, tc.status, got.body)
		} else if tc.status == http.StatusForbidden && !strings.Contains(got.body, "It came from another site.") {
			t.Errorf("%s: %s: %s", tc.header, tc.value, got.body)
		}
	}
	// A page is shown to any site.
	b.header.Set("Sec-Fetch-Site", "cross-site")
	b.get("/login").want(t, http.StatusOK, "/login")
	if !hasReason(h.logged("request refused"), "cross_origin") {
		t.Error("no cross-origin refusal logged")
	}
}

// noRedirects is a client that shows a redirect rather than follow it.
var noRedirects = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func TestHeaders(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"Content-Security-Policy": "default-src 'none'; style-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'",
		"Referrer-Policy":         "no-referrer",
		"X-Content-Type-Options":  "nosniff",
		"Cache-Control":           "no-store",
	}
	for _, tc := range []struct {
		publicURL string
		hsts      string
	}{
		{"http://localhost:8080", ""},
		{"https://hub.example", "max-age=31536000"},
	} {
		h := startHub(t, func(o *server.Options) { o.Config.PublicURL = tc.publicURL })
		for _, page := range []struct {
			method, path string
			status       int
		}{
			{http.MethodGet, "/login", http.StatusOK},
			{http.MethodGet, "/style.css", http.StatusOK},
			{http.MethodGet, "/", http.StatusSeeOther},
			{http.MethodGet, "/nope", http.StatusNotFound},
			{http.MethodGet, "/signup", http.StatusForbidden},
			{http.MethodPost, "/login", http.StatusUnprocessableEntity},
		} {
			req, err := http.NewRequest(page.method, h.base+page.path, strings.NewReader("username=a&password=b"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp, err := noRedirects.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != page.status {
				t.Errorf("%s %s: status %d, want %d", page.method, page.path, resp.StatusCode, page.status)
			}
			for k, v := range want {
				if got := resp.Header.Get(k); got != v {
					t.Errorf("%s %s: %s is %q, want %q", page.method, page.path, k, got, v)
				}
			}
			if got := resp.Header.Get("Strict-Transport-Security"); got != tc.hsts {
				t.Errorf("%s %s with %s: Strict-Transport-Security %q, want %q", page.method, page.path, tc.publicURL, got, tc.hsts)
			}
		}
	}
	h := startHub(t, nil)
	r := h.browser().get("/style.css")
	if r.status != http.StatusOK || !strings.HasPrefix(r.header.Get("Content-Type"), "text/css") || !strings.Contains(r.body, "body {") {
		t.Errorf("the stylesheet: %d %s", r.status, r.header.Get("Content-Type"))
	}
}

func TestSessionCookie(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	h.secret(password)
	b := h.browser()
	form := b.get(h.invite())
	req, err := http.NewRequest(http.MethodPost, h.base+"/signup", strings.NewReader("username=alice&password="+strings.ReplaceAll(password, " ", "+")+"&password2="+strings.ReplaceAll(password, " ", "+")+"&invite="+inviteOf(t, form)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := noRedirects.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	cookies := resp.Cookies()
	if resp.StatusCode != http.StatusSeeOther || len(cookies) != 1 {
		t.Fatalf("sign-up: %d, cookies %v", resp.StatusCode, cookies)
	}
	c := cookies[0]
	h.secret(c.Value)
	if c.Name != "wdx_hub_session" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Value == "" {
		t.Errorf("cookie %+v", c)
	}
}

func inviteOf(t *testing.T, r response) string {
	t.Helper()
	i := strings.Index(r.body, `name="invite" value="`)
	if i < 0 {
		t.Fatalf("no invite on %s", r.body)
	}
	rest := r.body[i+len(`name="invite" value="`):]
	return rest[:strings.Index(rest, `"`)]
}

// After five wrong tries in a row each next waits, from a second doubling,
// until one succeeds; the first refusal of each lockout is audited.
func TestLockout(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	p := h.signUp(h.browser(), "alice")
	b := h.browser()
	form := b.get("/login")
	wrong := map[string]string{"username": "alice", "password": "wrong password!"}
	for i := range 5 {
		if got := b.submit(form, "/login", wrong); got.status != http.StatusUnprocessableEntity {
			t.Fatalf("wrong try %d: %d", i+1, got.status)
		}
	}
	locked := b.submit(form, "/login", wrong)
	locked.want(t, http.StatusTooManyRequests, "", "Wait 1 second and try again.")
	if locked.header.Get("Retry-After") != "1" {
		t.Errorf("Retry-After %q", locked.header.Get("Retry-After"))
	}
	// Refused, even with the right password, which is not checked.
	b.submit(form, "/login", map[string]string{"username": "ALICE", "password": p.password}).want(t, http.StatusTooManyRequests, "")
	if got := h.actions(); count(got, account.ActionLockedOut) != 1 {
		t.Fatalf("actions %v", got)
	}
	h.clock.advance(time.Second)
	b.submit(form, "/login", wrong).want(t, http.StatusUnprocessableEntity, "")
	// The sixth wrong try waits twice as long.
	h.clock.advance(time.Second)
	b.submit(form, "/login", wrong).want(t, http.StatusTooManyRequests, "", "Wait 1 second")
	if got := h.actions(); count(got, account.ActionLockedOut) != 2 {
		t.Fatalf("actions %v", got)
	}
	h.clock.advance(time.Second)
	// The right password alone does not end the streak: the code counts
	// too.
	second := b.submit(form, "/login", map[string]string{"username": "alice", "password": p.password})
	second.want(t, http.StatusOK, "/login/second-factor")
	b.submit(second, "/login/second-factor", map[string]string{"code": "000000"}).want(t, http.StatusUnprocessableEntity, "")
	b.submit(second, "/login/second-factor", map[string]string{"code": "000000"}).want(t, http.StatusTooManyRequests, "", "Wait 4 seconds")
	// The code is computed, and the clock moves past the wait.
	b.submit(second, "/login/second-factor", map[string]string{"code": h.code(p.key)}).want(t, http.StatusOK, "/security")
	other := h.browser()
	for i := range 5 {
		if got := other.submit(form, "/login", wrong); got.status != http.StatusUnprocessableEntity {
			t.Fatalf("after a success, wrong try %d: %d", i+1, got.status)
		}
	}
	if !hasReason(h.logged("login refused"), "rate_limited") {
		t.Error("no login refused for rate_limited logged")
	}
}

// Wrong codes at enrolment count as wrong codes at login do.
func TestEnrolmentLockout(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	h.secret(password)
	b := h.browser()
	enrol := b.submit(b.get(h.invite()), "/signup", map[string]string{"username": "alice", "password": password, "password2": password})
	h.secret(keyOf(t, enrol))
	for i := range 5 {
		if got := b.submit(enrol, "/enrol", map[string]string{"code": "000000"}); got.status != http.StatusUnprocessableEntity {
			t.Fatalf("wrong code %d: %d", i+1, got.status)
		}
	}
	b.submit(enrol, "/enrol", map[string]string{"code": "000000"}).want(t, http.StatusTooManyRequests, "", "Wait 1 second")
}

// A wrong current password on the Security page counts too: a browser left
// logged in does not let its password be guessed.
func TestSecurityLockout(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	b := h.browser()
	p := h.signUp(b, "alice")
	page := b.get("/security")
	for i, action := range []string{"/security/authenticator", "/security/recovery-codes", "/security/authenticator", "/security/recovery-codes", "/security/authenticator"} {
		if got := b.submit(page, action, map[string]string{"current": "wrong password!"}); got.status != http.StatusUnprocessableEntity {
			t.Fatalf("wrong password %d: %d", i+1, got.status)
		}
	}
	b.submit(page, "/security/recovery-codes", map[string]string{"current": p.password}).want(t, http.StatusTooManyRequests, "", "Wait 1 second")
	// The login of alice waits too.
	o := h.browser()
	o.submit(o.get("/login"), "/login", map[string]string{"username": "alice", "password": p.password}).want(t, http.StatusTooManyRequests, "")
}

// An address posts at most 20 passwords, codes or tokens a minute, wrong
// invite and reset tokens counted, and 10 sign-ups.
func TestAddressLimits(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	b := h.browser()
	form := b.get("/login")
	for i := range 20 {
		if got := b.submit(form, "/login", map[string]string{"username": fmt.Sprintf("nobody-%d", i), "password": "wrong password!"}); got.status != http.StatusUnprocessableEntity {
			t.Fatalf("try %d: %d", i+1, got.status)
		}
	}
	b.submit(form, "/login", map[string]string{"username": "somebody", "password": "wrong password!"}).want(t, http.StatusTooManyRequests, "", "Wait 60 seconds")
	h.browser().get("/reset?token=nope").want(t, http.StatusTooManyRequests, "")
	h.clock.advance(time.Minute)
	b.submit(form, "/login", map[string]string{"username": "somebody", "password": "wrong password!"}).want(t, http.StatusUnprocessableEntity, "")

	tokens := startHub(t, nil)
	b = tokens.browser()
	for i := range 10 {
		b.get("/signup?invite=nope").want(t, http.StatusNotFound, "")
		if i < 9 {
			b.get("/reset?token=nope").want(t, http.StatusNotFound, "")
		}
	}
	b.post("/reset", map[string]string{"token": "nope", "password": "x", "password2": "x"}).want(t, http.StatusNotFound, "")
	// The twenty-first is refused, though its invite is good.
	b.get(tokens.invite()).want(t, http.StatusTooManyRequests, "")

	signUps := startHub(t, nil)
	b = signUps.browser()
	signUp := b.get(signUps.invite())
	differ := map[string]string{"username": "bob", "password": "one password, long", "password2": "another, long"}
	for i := range 10 {
		if got := b.submit(signUp, "/signup", differ); got.status != http.StatusUnprocessableEntity {
			t.Fatalf("sign-up %d: %d", i+1, got.status)
		}
	}
	b.submit(signUp, "/signup", differ).want(t, http.StatusTooManyRequests, "")
}

// X-Forwarded-For is believed only from a trusted proxy, and then its
// rightmost address that is not one.
func TestForwardedFor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		trusted []netip.Prefix
		want    string
	}{
		{nil, "127.0.0.1"},
		{[]netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8")}, "203.0.113.9"},
	} {
		h := startHub(t, func(o *server.Options) { o.Config.TrustedProxies = tc.trusted })
		b := h.browser()
		b.header.Set("X-Forwarded-For", "198.51.100.7, 203.0.113.9, 10.1.2.3")
		h.signUp(b, "alice")
		rows := rowPattern.FindAllStringSubmatch(b.get("/security").body, -1)
		if len(rows) != 1 || !strings.Contains(rows[0][2], "<td>"+tc.want+"</td>") {
			t.Errorf("trusting %v: browsers %v, want %s", tc.trusted, rows, tc.want)
		}
	}

	// The limits count the client's address, not the proxy's.
	h := startHub(t, func(o *server.Options) {
		o.Config.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	})
	one, two := h.browser(), h.browser()
	one.header.Set("X-Forwarded-For", "203.0.113.1")
	two.header.Set("X-Forwarded-For", "203.0.113.2")
	form := one.get("/login")
	for i := range 20 {
		one.submit(form, "/login", map[string]string{"username": fmt.Sprintf("nobody-%d", i), "password": "wrong password!"})
	}
	one.submit(form, "/login", map[string]string{"username": "somebody", "password": "wrong password!"}).want(t, http.StatusTooManyRequests, "")
	two.submit(form, "/login", map[string]string{"username": "somebody", "password": "wrong password!"}).want(t, http.StatusUnprocessableEntity, "")
}

// next leads only to a path on the hub, through every step of the flow.
func TestNext(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	h.secret(password)
	b := h.browser()
	form := b.get(h.invite() + "&next=/devices%3Fcode%3D1")
	enrol := b.submit(form, "/signup", map[string]string{"username": "alice", "password": password, "password2": password})
	enrol.want(t, http.StatusOK, "/enrol?next=%2Fdevices%3Fcode%3D1")
	key := keyOf(t, enrol)
	h.secret(key)
	codes := b.submit(enrol, "/enrol", map[string]string{"code": h.code(key)})
	h.secret(codesOf(t, codes)...)
	// Step 5's device page is not built: it is not found, where it will be.
	b.submit(codes, "/recovery-codes/saved", map[string]string{"saved": "yes"}).want(t, http.StatusNotFound, "/devices?code=1")

	p := person{username: "alice", password: password, key: key}
	for _, next := range []string{"//evil.example/x", "https://evil.example/", "/\\evil.example", "evil.example"} {
		o := h.browser()
		second := o.submit(o.get("/login?next="+next), "/login", map[string]string{"username": p.username, "password": p.password})
		second.want(t, http.StatusOK, "/login/second-factor")
		o.submit(second, "/login/second-factor", map[string]string{"code": h.code(p.key)}).want(t, http.StatusOK, "/security")
	}
	if len(h.logged("next refused")) == 0 {
		t.Error("no next refused logged")
	}
	o := h.browser()
	second := o.submit(o.get("/login?next=/elsewhere"), "/login", map[string]string{"username": p.username, "password": p.password})
	o.submit(second, "/login/second-factor", map[string]string{"code": h.code(p.key)}).want(t, http.StatusNotFound, "/elsewhere")
}

// heldHashes holds every password hash until released, counting those
// running at once.
type heldHashes struct {
	mu       sync.Mutex
	running  int
	most     int
	ran      int
	started  chan struct{}
	released chan struct{}
}

func newHeldHashes() *heldHashes {
	return &heldHashes{started: make(chan struct{}, 100), released: make(chan struct{})}
}

func (h *heldHashes) around(hash func()) {
	h.mu.Lock()
	h.running++
	h.ran++
	h.most = max(h.most, h.running)
	h.mu.Unlock()
	h.started <- struct{}{}
	<-h.released
	hash()
	h.mu.Lock()
	h.running--
	h.mu.Unlock()
}

func (h *heldHashes) counts() (most, ran int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.most, h.ran
}

// 40 logins at once from one address: 20 are tried, as the address allows,
// and of those two are hashed at once and sixteen wait; the rest are refused
// as a rate limit refuses.
func TestLoginBurst(t *testing.T) {
	t.Parallel()
	held := newHeldHashes()
	h := startHub(t, func(o *server.Options) { o.AroundHash = held.around })
	form := h.browser().get("/login")
	const logins = 40
	statuses := make(chan int, logins)
	for range logins {
		b := h.browser()
		go func() {
			statuses <- b.submit(form, "/login", map[string]string{"username": "alice", "password": "wrong password!"}).status
		}()
	}
	// Before any hash ends, 22 are refused, those over the address's 20 and
	// the two tried for which no slot is free nor a place to wait, and 2 are
	// hashed: 24 events in all, which happen however many hashes run.
	got := map[int]int{}
	for events := 0; events < 24; events++ {
		select {
		case status := <-statuses:
			got[status]++
		case <-held.started:
		}
	}
	close(held.released)
	for range logins - got[http.StatusTooManyRequests] {
		got[<-statuses]++
	}
	if got[http.StatusTooManyRequests] != 22 || got[http.StatusUnprocessableEntity] != 18 {
		t.Errorf("statuses %v, want 22 refused as too many and 18 tried", got)
	}
	if most, ran := held.counts(); most != 2 || ran != 18 {
		t.Errorf("%d hashes at most at once, %d in all; want 2, 18", most, ran)
	}
	if len(h.logged("password not checked")) != 2 {
		t.Error("the busy tries not logged")
	}
}

// Wrong passwords from one address lock the username out from it alone: the
// right password from another still logs in.
func TestLockoutPerAddress(t *testing.T) {
	t.Parallel()
	h := startHub(t, func(o *server.Options) {
		o.Config.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	})
	owner, guesser := h.browser(), h.browser()
	owner.header.Set("X-Forwarded-For", "203.0.113.1")
	guesser.header.Set("X-Forwarded-For", "203.0.113.2")
	p := h.signUp(owner, "alice")
	form := guesser.get("/login")
	wrong := map[string]string{"username": "alice", "password": "wrong password!"}
	for range 5 {
		guesser.submit(form, "/login", wrong).want(t, http.StatusUnprocessableEntity, "")
	}
	guesser.submit(form, "/login", wrong).want(t, http.StatusTooManyRequests, "")
	other := h.browser()
	other.header.Set("X-Forwarded-For", "203.0.113.1")
	other.submit(other.get("/login"), "/login", map[string]string{"username": "alice", "password": p.password}).want(t, http.StatusOK, "/login/second-factor")
}

// A username no user can have, however long, is a wrong password with no
// hash and no streak.
func TestLoginInvalidUsername(t *testing.T) {
	t.Parallel()
	held := newHeldHashes()
	close(held.released)
	h := startHub(t, func(o *server.Options) { o.AroundHash = held.around })
	b := h.browser()
	form := b.get("/login")
	long := map[string]string{"username": strings.Repeat("a", 60_000), "password": "wrong password!"}
	for i := range 5 + 2 {
		if got := b.submit(form, "/login", long); got.status != http.StatusUnprocessableEntity {
			t.Fatalf("try %d: %d", i+1, got.status)
		}
	}
	if _, ran := held.counts(); ran != 0 {
		t.Errorf("%d hashes run", ran)
	}
	if got := h.actions(); count(got, account.ActionLockedOut) != 0 {
		t.Errorf("actions %v", got)
	}
}

// Wrong tries of an unknown username lock it out from the address, but no
// lockout is audited: only a user's.
func TestUnknownLockoutNotAudited(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	b := h.browser()
	form := b.get("/login")
	wrong := map[string]string{"username": "nobody", "password": "wrong password!"}
	for range 5 {
		b.submit(form, "/login", wrong).want(t, http.StatusUnprocessableEntity, "")
	}
	b.submit(form, "/login", wrong).want(t, http.StatusTooManyRequests, "")
	if got := h.actions(); count(got, account.ActionLockedOut) != 0 {
		t.Errorf("actions %v", got)
	}
}

// A user's wrong passwords from any address are audited once they reach 50
// in a day, once, refusing nothing more.
func TestPasswordAttacked(t *testing.T) {
	t.Parallel()
	h := startHub(t, func(o *server.Options) {
		o.Config.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	})
	p := h.signUp(h.browser(), "alice")
	wrong := map[string]string{"username": "alice", "password": "wrong password!"}
	for i := range account.AttackedTries + 5 {
		b := h.browser()
		b.header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i/5))
		b.submit(b.get("/login"), "/login", wrong).want(t, http.StatusUnprocessableEntity, "")
		if got := count(h.actions(), account.ActionPasswordAttacked); got != min(max(i+2-account.AttackedTries, 0), 1) {
			t.Fatalf("after %d wrong passwords, %d attacks audited", i+1, got)
		}
	}
	b := h.browser()
	b.header.Set("X-Forwarded-For", "198.51.100.1")
	b.submit(b.get("/login"), "/login", map[string]string{"username": "alice", "password": p.password}).want(t, http.StatusOK, "/login/second-factor")
}
