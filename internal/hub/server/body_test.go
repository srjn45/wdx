package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/logging"
)

// counted counts the bytes read from it.
type counted struct {
	io.Reader
	n int64
}

func (c *counted) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *counted) Close() error { return nil }

func TestReadBodyBounded(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		size     int64
		tooLarge bool
	}{
		{"at the limit", maxBody, false},
		{"over it", 1 << 20, true},
	} {
		body := &counted{Reader: bytes.NewReader(make([]byte, tc.size))}
		b, tooLarge, err := readBody(httptest.NewRecorder(), body, maxBody)
		if err != nil || tooLarge != tc.tooLarge {
			t.Fatalf("%s: too large %v, %v; want %v", tc.name, tooLarge, err, tc.tooLarge)
		}
		if body.n > maxBody+1 {
			t.Errorf("%s: read %d bytes, want at most %d", tc.name, body.n, maxBody+1)
		}
		if want := tc.size; tc.tooLarge {
			if b != nil {
				t.Errorf("%s: kept %d bytes of a body too large", tc.name, len(b))
			}
		} else if int64(len(b)) != want {
			t.Errorf("%s: got %d bytes, want %d", tc.name, len(b), want)
		}
	}
}

// receivedLine is the line request received of the request logged under id.
func receivedLine(t *testing.T, home, id string) map[string]any {
	t.Helper()
	for _, line := range logLines(t, home) {
		if line["msg"] == "request received" && line["debug_id"] == id {
			return line
		}
	}
	t.Fatalf("no request received under %s", id)
	return nil
}

func TestLargeBodyNotRead(t *testing.T) {
	t.Parallel()
	th := start(t, nil)
	large := string(make([]byte, 1<<20))
	for _, tc := range []struct {
		name, path string
		status     int
		code       hubapi.ErrorCode
	}{
		{"no operation", "/v1/no-such-operation", http.StatusNotFound, hubapi.ErrorCodeNotFound},
		{"startNodeLogin", "/v1/node-logins", http.StatusBadRequest, hubapi.ErrorCodeUsage},
	} {
		id := logging.NewDebugID()
		resp, b := do(t, th.web, http.MethodPost, "http://"+th.addrs.Web+tc.path, large, http.Header{hubapi.DebugIDHeader: {id}})
		if resp.StatusCode != tc.status || problemCode(t, b) != tc.code {
			t.Fatalf("%s: %d %s, want %d %s", tc.name, resp.StatusCode, b, tc.status, tc.code)
		}
		line := receivedLine(t, th.home, id)
		if tc.code == hubapi.ErrorCodeNotFound {
			if _, ok := line["body"]; ok || line["body_length"] != float64(1<<20) {
				t.Errorf("%s: want the length logged, not the body: %v", tc.name, line)
			}
			continue
		}
		if line["body"] != "" || line["body_too_large"] != true {
			t.Errorf("%s: want no body logged and body_too_large: %.200v", tc.name, line)
		}
	}
}

func TestIdleTimeouts(t *testing.T) {
	t.Parallel()
	th := start(t, nil)
	for e, want := range map[endpoint]bool{endpointWeb: true, endpointAdmin: true, endpointNodes: false} {
		srv := th.server.httpServer(e)
		if (srv.IdleTimeout != 0) != want {
			t.Errorf("%s: idle timeout %v, want one: %v", e, srv.IdleTimeout, want)
		}
		if srv.ReadTimeout != 0 {
			t.Errorf("%s: read timeout %v, which would stay on a hijacked connection", e, srv.ReadTimeout)
		}
	}
}
