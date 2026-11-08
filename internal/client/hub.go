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

// StartHubCode starts setting up the node's authenticator code, replacing
// the one set up when replace says to. The secret is a secret: never log it.
func (c *Client) StartHubCode(ctx context.Context, replace bool) (api.HubCodePendingEnvelope, error) {
	r, err := c.api.StartHubCodeWithResponse(ctx, api.HubCodeStart{Replace: &replace})
	if err != nil {
		return api.HubCodePendingEnvelope{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON201)
}

// VerifyHubCode finishes the setup with a code of the pending secret. The
// code and the recovery key returned are secrets: never log them.
func (c *Client) VerifyHubCode(ctx context.Context, code string) (api.HubCodeSetUp, error) {
	r, err := c.api.VerifyHubCodeWithResponse(ctx, api.HubCodeCheck{Code: code})
	if err != nil {
		return api.HubCodeSetUp{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON201)
}

// RecoverHubCode starts a new setup with the recovery key, a secret: never
// log it, nor the secret returned.
func (c *Client) RecoverHubCode(ctx context.Context, key string) (api.HubCodePendingEnvelope, error) {
	r, err := c.api.RecoverHubCodeWithResponse(ctx, api.HubCodeRecovery{RecoveryKey: key})
	if err != nil {
		return api.HubCodePendingEnvelope{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON201)
}

// VerifyHubAudit walks the node's audit chain.
func (c *Client) VerifyHubAudit(ctx context.Context) (api.HubAuditEnvelope, error) {
	r, err := c.api.VerifyHubAuditWithResponse(ctx)
	if err != nil {
		return api.HubAuditEnvelope{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// HubSessions is the sessions open, both ways.
func (c *Client) HubSessions(ctx context.Context) (api.HubSessionList, error) {
	r, err := c.api.ListHubSessionsWithResponse(ctx)
	if err != nil {
		return api.HubSessionList{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// OpenHubSession opens a session into the node ref names with its code, a
// secret: never log it.
func (c *Client) OpenHubSession(ctx context.Context, ref, code string) (api.HubSessionResult, error) {
	r, err := c.api.OpenHubSessionWithResponse(ctx, api.HubSessionOpen{Node: ref, Code: code})
	if err != nil {
		return api.HubSessionResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON201, r.JSON200)
}

// CloseHubSession closes the session open of id.
func (c *Client) CloseHubSession(ctx context.Context, id string) (api.HubSessionResult, error) {
	r, err := c.api.CloseHubSessionWithResponse(ctx, id)
	if err != nil {
		return api.HubSessionResult{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}

// CloseHubSessions closes every session open.
func (c *Client) CloseHubSessions(ctx context.Context) (api.HubSessionsClosed, error) {
	r, err := c.api.CloseHubSessionsWithResponse(ctx)
	if err != nil {
		return api.HubSessionsClosed{}, err
	}
	return result(r.HTTPResponse, r.Body, r.ApplicationproblemJSONDefault, r.JSON200)
}
