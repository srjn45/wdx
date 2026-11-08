package hubtest

import (
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
)

// Browser is a person's browser on the hub: a cookie jar, and the user it
// logs in as when a page asks. Its methods return errors rather than fail the
// test, so that they may run beside a command waiting on the login.
type Browser struct {
	hub    *Hub
	user   User
	client *http.Client
}

// Browser is a browser of u, logged in to nothing yet.
func (h *Hub) Browser(u User) *Browser {
	jar, err := cookiejar.New(nil)
	if err != nil {
		h.t.Fatal(err)
	}
	b := &Browser{hub: h, user: u}
	b.client = &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		// The browser leaves the hub only for a node's callback, which the
		// caller follows if it wants to.
		if req.URL.Host != b.hubHost() {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	return b
}

func (b *Browser) hubHost() string {
	u, _ := url.Parse(b.hub.URL)
	return u.Host
}

// ErrNameTaken is an approval the page refused because the account has a
// node of that name; a Page holds the page.
var ErrNameTaken = errors.New("hubtest: the account has a node of that name")

// Page is a page the browser ended on that is not what was wanted.
type Page struct {
	Status int
	Path   string
	Body   string
}

func (p *Page) Error() string {
	return fmt.Sprintf("hubtest: ended on %s with %d:\n%s", p.Path, p.Status, p.Body)
}

// page is a page as the browser shows it, once it followed the redirects on
// the hub.
type page struct {
	status   int
	path     string
	location string // where it would go off the hub
	body     string
}

// onHub is link, a link the hub handed out under PublicURL, at the hub's web
// endpoint.
func (b *Browser) onHub(link string) (string, error) {
	rest, ok := strings.CutPrefix(link, PublicURL)
	if !ok {
		if strings.HasPrefix(link, b.hub.URL) {
			return link, nil
		}
		return "", fmt.Errorf("hubtest: %s is not a link of the hub", link)
	}
	return b.hub.URL + rest, nil
}

func (b *Browser) do(method, link string, form url.Values) (page, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, link, body)
	if err != nil {
		return page{}, err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return page{}, err
	}
	defer resp.Body.Close()
	text, err := io.ReadAll(resp.Body)
	if err != nil {
		return page{}, err
	}
	b.noteSession()
	p := page{status: resp.StatusCode, path: resp.Request.URL.RequestURI(), body: string(text)}
	if resp.StatusCode == http.StatusSeeOther || resp.StatusCode == http.StatusFound {
		p.location = resp.Header.Get("Location")
	}
	return p, nil
}

// noteSession names the session's cookie a secret.
func (b *Browser) noteSession() {
	u, _ := url.Parse(b.hub.URL)
	for _, c := range b.client.Jar.Cookies(u) {
		b.hub.Secret(c.Value)
	}
}

var (
	formPattern  = regexp.MustCompile(`(?s)<form method="post" action="([^"]*)">(.*?)</form>`)
	inputPattern = regexp.MustCompile(`<input ([^>]*)>`)
	attrPattern  = regexp.MustCompile(`([a-z_]+)="([^"]*)"`)
)

// submit posts the form of p whose action is action, with its fields'
// values and those of fill.
func (b *Browser) submit(p page, action string, fill map[string]string) (page, error) {
	for _, m := range formPattern.FindAllStringSubmatch(p.body, -1) {
		if html.UnescapeString(m[1]) != action {
			continue
		}
		form := url.Values{}
		for _, in := range inputPattern.FindAllStringSubmatch(m[2], -1) {
			attrs := map[string]string{}
			for _, a := range attrPattern.FindAllStringSubmatch(in[1], -1) {
				attrs[a[1]] = html.UnescapeString(a[2])
			}
			if attrs["type"] != "checkbox" {
				form.Set(attrs["name"], attrs["value"])
			}
		}
		for name, v := range fill {
			form.Set(name, v)
		}
		return b.do(http.MethodPost, b.hub.URL+action, form)
	}
	return page{}, &Page{Status: p.status, Path: p.path, Body: p.body}
}

// open opens link and logs in when the hub asks, ending on the page link
// leads to.
func (b *Browser) open(link string) (page, error) {
	p, err := b.do(http.MethodGet, link, nil)
	if err != nil {
		return page{}, err
	}
	if strings.HasPrefix(p.path, "/login?") || p.path == "/login" {
		if p, err = b.submit(p, "/login", map[string]string{"username": b.user.Username, "password": b.user.Password}); err != nil {
			return page{}, err
		}
		if !strings.HasPrefix(p.path, "/login/second-factor") {
			return page{}, &Page{Status: p.status, Path: p.path, Body: p.body}
		}
		if p, err = b.submit(p, "/login/second-factor", map[string]string{"code": b.hub.Code(b.user)}); err != nil {
			return page{}, err
		}
	}
	return p, nil
}

// approvalPage is the approval page of the login at loginURL, or of the
// device login whose code is userCode when loginURL is empty.
func (b *Browser) approvalPage(loginURL, userCode string) (page, error) {
	if userCode != "" {
		b.hub.Secret(userCode)
		form, err := b.open(b.hub.URL + "/device")
		if err != nil {
			return page{}, err
		}
		return b.submit(form, "/device", map[string]string{"code": userCode})
	}
	link, err := b.onHub(loginURL)
	if err != nil {
		return page{}, err
	}
	return b.open(link)
}

// decide approves, with name when it is not empty, or denies the login,
// returning where the browser would go off the hub: the callback, for the
// browser flow.
func (b *Browser) decide(loginURL, userCode string, approve bool, name string) (*url.URL, error) {
	p, err := b.approvalPage(loginURL, userCode)
	if err != nil {
		return nil, err
	}
	action, fill := "/nodes/deny", map[string]string{}
	if approve {
		action = "/nodes/approve"
		if name != "" {
			fill["name"] = name
		}
	}
	if p, err = b.submit(p, action, fill); err != nil {
		return nil, err
	}
	if p.location != "" {
		u, err := url.Parse(p.location)
		if err == nil {
			b.hub.Secret(u.Query().Get("code"))
		}
		return u, err
	}
	if p.status == http.StatusConflict && strings.Contains(p.body, "choose another name") {
		return nil, fmt.Errorf("%w: %w", ErrNameTaken, &Page{Status: p.status, Path: p.path, Body: p.body})
	}
	if p.status != http.StatusOK {
		return nil, &Page{Status: p.status, Path: p.path, Body: p.body}
	}
	return nil, nil
}

// Approve approves the browser login at loginURL, its name changed to name
// unless empty, logging in first when the hub asks, and returns the callback
// the hub sends the browser to with the code and state; the caller follows
// it, as the browser would, with FollowCallback. It fails with ErrNameTaken
// when the account has a node of the name.
func (b *Browser) Approve(loginURL, name string) (*url.URL, error) {
	return b.decide(loginURL, "", true, name)
}

// Deny denies the browser login at loginURL, and returns the callback the
// hub sends the browser to with the error.
func (b *Browser) Deny(loginURL string) (*url.URL, error) {
	return b.decide(loginURL, "", false, "")
}

// ApproveCode approves the device login whose user code is userCode, typed
// on the device page, its name changed to name unless empty.
func (b *Browser) ApproveCode(userCode, name string) error {
	_, err := b.decide("", userCode, true, name)
	return err
}

// DenyCode denies the device login whose user code is userCode.
func (b *Browser) DenyCode(userCode string) error {
	_, err := b.decide("", userCode, false, "")
	return err
}

// FollowCallback goes to callback as the browser would once sent there,
// returning the page's body: what the node's loopback shows.
func (b *Browser) FollowCallback(callback *url.URL) (string, error) {
	resp, err := http.Get(callback.String())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}
