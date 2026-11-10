package hubcli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/waittest"
)

// TestRunClosesClient checks a run closes its connection to the hub when it
// returns, while the hub goes on serving: a hub that answers on its admin
// socket and says when a connection closes, which only the client can do.
func TestRunClosesClient(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	l, err := net.Listen("unix", hubconfig.SocketPath(home))
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{}, 1)
	s := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(hubapi.VersionHeader, identity.Version)
			_ = json.NewEncoder(w).Encode(hubapi.Version{Name: name, Version: identity.Version, ApiVersion: "1"})
		}),
		ConnState: func(_ net.Conn, state http.ConnState) {
			if state == http.StateClosed {
				closed <- struct{}{}
			}
		},
	}
	go func() { _ = s.Serve(l) }()
	t.Cleanup(func() { _ = s.Close() })

	var stdout, stderr bytes.Buffer
	if exit := Run(context.Background(), []string{"version", "--json"}, testEnv(t, home, &stdout, &stderr)); exit != clikit.ExitOK {
		t.Fatalf("want version to succeed, got exit %d: %s%s", exit, stdout.String(), stderr.String())
	}
	select {
	case <-closed:
	case <-waittest.After(t):
		t.Fatal("the run returned with its connection to the hub still open")
	}
}
