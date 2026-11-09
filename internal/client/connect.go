package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// Connect asks the daemon for a stream of the session open into node, by
// its name or "id:" and its id: the connection, switched to the stream, over
// which another client speaks HTTP to that node's API. With no session open
// it fails with session_required, as a *ProblemError.
func (c *Client) Connect(ctx context.Context, node string) (net.Conn, error) {
	return c.switched(ctx, "/v1/hub/nodes/"+url.PathEscape(node)+"/connect")
}

// AttachStream asks the daemon, through whatever the client dials, for a
// stream to the holder of the terminal with terminalID, an agent's or an
// editor's too: the connection, switched to it, speaks the holder's protocol
// from the client's hello on, as its socket does. It is for a client that
// cannot dial the socket, through a session into another node.
func (c *Client) AttachStream(ctx context.Context, terminalID string) (net.Conn, error) {
	return c.switched(ctx, "/v1/terminals/"+url.PathEscape("id:"+terminalID)+"/attach")
}

// switched sends POST path as an Upgrade on a connection of its own and
// returns it once switched, or the problem the daemon answered.
func (c *Client) switched(ctx context.Context, path string) (net.Conn, error) {
	d := c.doer
	d.inflight.Add(1)
	defer d.inflight.Done()
	start := time.Now()
	log := logging.FromContext(ctx)
	conn, err := d.dial(ctx)
	if err != nil {
		if d.opts.Dial != nil {
			log.Info("node unreachable", "method", http.MethodPost, "path", path, "node", d.opts.Node, "err", err)
			return nil, err
		}
		log.Info("daemon unreachable", "method", http.MethodPost, "path", path, "socket", d.opts.Socket, "err", err)
		if op, ok := errors.AsType[*net.OpError](err); ok {
			err = op.Err
		}
		return nil, &UnavailableError{Socket: d.opts.Socket, Err: err}
	}
	// ctx bounds the switch, not the stream after it.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	resp, in, err := d.upgrade(ctx, conn, path)
	if !stop() {
		conn.Close()
		return nil, ctx.Err()
	}
	if err != nil {
		conn.Close()
		if d.opts.Dial != nil {
			log.Info("node unreachable", "method", http.MethodPost, "path", path, "node", d.opts.Node, "err", err)
			p := api.NewProblem(api.ErrorCodeSessionClosed, fmt.Sprintf("the stream to node %s ended before it answered: %v", d.opts.Node, err))
			p.Details = &map[string]any{"node": d.opts.Node}
			return nil, &ProblemError{Problem: p}
		}
		log.Info("daemon unreachable", "method", http.MethodPost, "path", path, "socket", d.opts.Socket, "err", err)
		return nil, &UnavailableError{Socket: d.opts.Socket, Err: err}
	}
	if d.opts.Dial != nil {
		version := d.recordAPIVersion(resp)
		log.Info("node called", "method", http.MethodPost, "path", path, "status", resp.StatusCode, "node", d.opts.Node, "api_version", version, logging.Duration(start))
		if version < api.OldestCompatible {
			resp.Body.Close()
			conn.Close()
			return nil, d.hostTooOld(version)
		}
	} else {
		log.Info("daemon called", "method", http.MethodPost, "path", path, "status", resp.StatusCode, "daemon_version", resp.Header.Get(api.VersionHeader), logging.Duration(start))
	}
	if resp.StatusCode == http.StatusSwitchingProtocols {
		// The body is the stream, read through the connection.
		return &switchedConn{Conn: conn, in: in}, nil
	}
	defer resp.Body.Close()
	defer conn.Close()
	var p api.Problem
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil || p.Code == "" {
		return nil, fmt.Errorf("unexpected response from the daemon to POST %s: %s", path, resp.Status)
	}
	return nil, &ProblemError{Problem: p}
}

// dial opens a connection of its own to the daemon: its socket, or Dial's
// stream to another node's.
func (d *doer) dial(ctx context.Context) (net.Conn, error) {
	if d.opts.Dial != nil {
		return d.opts.Dial(ctx)
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", d.opts.Socket)
}

// upgrade sends the connect request on conn and reads its answer.
func (d *doer) upgrade(ctx context.Context, conn net.Conn, path string) (*http.Response, *bufio.Reader, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+identity.Name+path, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", api.StreamUpgrade)
	req.Header.Set(api.DebugIDHeader, d.opts.DebugID)
	req.Header.Set(api.VersionHeader, identity.Version)
	if d.opts.Dial != nil {
		req.Header.Set(api.APIVersionHeader, strconv.Itoa(api.CurrentVersion))
	}
	if d.opts.Quiet {
		req.Header.Set(api.LogLevelHeader, api.LogLevelDebug)
	}
	if err := req.Write(conn); err != nil {
		return nil, nil, err
	}
	in := bufio.NewReader(conn)
	resp, err := http.ReadResponse(in, req)
	return resp, in, err
}

// switchedConn reads first what the reader of the switch read ahead.
type switchedConn struct {
	net.Conn
	in *bufio.Reader
}

func (c *switchedConn) Read(b []byte) (int, error) { return c.in.Read(b) }
