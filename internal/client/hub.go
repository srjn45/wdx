package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/srjn45/wdx/internal/api"
)

// Hub is this node's status on the hub.
func (c *Client) Hub(ctx context.Context) (api.HubEnvelope, error) {
	r, err := c.api.GetHubWithResponse(ctx)
	if err != nil {
		return api.HubEnvelope{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// StartHubLogin starts a login, which HubLoginEvents follows. Its user code
// is a secret: never log it.
func (c *Client) StartHubLogin(ctx context.Context, req api.HubLoginStart) (api.HubLoginEnvelope, error) {
	r, err := c.api.StartHubLoginWithResponse(ctx, req)
	if err != nil {
		return api.HubLoginEnvelope{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON201)
}

// SendHubLoginCode gives the daemon what the login's callback received. The
// code is a secret: never log it.
func (c *Client) SendHubLoginCode(ctx context.Context, code api.HubLoginCode) (api.HubLoginEvent, error) {
	r, err := c.api.SendHubLoginCodeWithResponse(ctx, code)
	if err != nil {
		return api.HubLoginEvent{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// CancelHubLogin cancels the login of id.
func (c *Client) CancelHubLogin(ctx context.Context, id string) (api.HubLoginEvent, error) {
	r, err := c.api.CancelHubLoginWithResponse(ctx, &api.CancelHubLoginParams{Login: id})
	if err != nil {
		return api.HubLoginEvent{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// LogoutHub leaves the hub.
func (c *Client) LogoutHub(ctx context.Context) (api.HubLogoutResult, error) {
	r, err := c.api.LogoutHubWithResponse(ctx)
	if err != nil {
		return api.HubLogoutResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// HubNodes lists the account's nodes.
func (c *Client) HubNodes(ctx context.Context) (api.HubNodeList, error) {
	r, err := c.api.ListHubNodesWithResponse(ctx)
	if err != nil {
		return api.HubNodeList{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// HubNode is the account's node ref names: its name, or id: and its id.
func (c *Client) HubNode(ctx context.Context, ref string) (api.HubNodeEnvelope, error) {
	r, err := c.api.GetHubNodeWithResponse(ctx, ref)
	if err != nil {
		return api.HubNodeEnvelope{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// RemoveHubNode revokes the account's node ref names.
func (c *Client) RemoveHubNode(ctx context.Context, ref string) (api.HubNodeResult, error) {
	r, err := c.api.RemoveHubNodeWithResponse(ctx, ref)
	if err != nil {
		return api.HubNodeResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// HubLoginStream is a login followed with streamHubLogin: every event is its
// state.
type HubLoginStream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
}

// HubLoginEvents follows the login of id, until it ends, ctx ends or Close.
func (c *Client) HubLoginEvents(ctx context.Context, id string) (*HubLoginStream, error) {
	resp, err := c.api.StreamHubLogin(ctx, &api.StreamHubLoginParams{Login: id})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		r, err := api.ParseStreamHubLoginResponse(resp)
		if err != nil {
			return nil, err
		}
		_, err = result[struct{}](resp, r.Body, r.ApplicationproblemJSONDefault)
		return nil, err
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 4<<10), 1<<20)
	return &HubLoginStream{body: resp.Body, scanner: scanner}, nil
}

// Next waits for the login's next state. It returns io.EOF once the stream
// has ended: after a state that is not pending, or when the daemon ended it.
func (s *HubLoginStream) Next() (api.HubLoginEvent, error) {
	event, data, err := nextEvent(s.scanner)
	if err != nil {
		return api.HubLoginEvent{}, err
	}
	if event != "login" {
		return api.HubLoginEvent{}, fmt.Errorf("unexpected event %q from the daemon", event)
	}
	var e api.HubLoginEvent
	if err := json.Unmarshal([]byte(data), &e); err != nil {
		return api.HubLoginEvent{}, fmt.Errorf("login event unreadable: %w", err)
	}
	return e, nil
}

// Close stops following the login.
func (s *HubLoginStream) Close() error { return s.body.Close() }
