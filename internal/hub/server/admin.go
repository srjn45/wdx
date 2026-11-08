package server

import (
	"context"
	"net/url"
	"time"

	"github.com/srjn45/wdx/internal/hub/account"
	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/hub/node"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/logging"
)

// DefaultInviteLife is how long an invite works when the admin says nothing.
const DefaultInviteLife = 24 * time.Hour

func (a *api) CreateInvite(ctx context.Context, req hubapi.CreateInviteRequestObject) (hubapi.CreateInviteResponseObject, error) {
	life := DefaultInviteLife
	if s := req.Body.ExpiresSeconds; s != nil {
		// Capped past the longest life, which the store refuses, so that a
		// Duration does not overflow.
		life = time.Duration(min(*s, int64(account.MaxInviteLife/time.Second)+1)) * time.Second
	}
	inv, err := a.server.accounts.CreateInvite(ctx, account.Admin, life)
	if err != nil {
		return nil, err
	}
	return hubapi.CreateInvite201JSONResponse{
		Result: hubapi.ResultInvited,
		Invite: hubapi.Invite{Link: a.link("/signup", "invite", inv.Token), ExpiresAt: inv.ExpiresAt},
	}, nil
}

// link is the page at path of the hub's public address, with the token in
// the query parameter key.
func (a *api) link(path, key, token string) string {
	return a.server.opts.Config.PublicURL + path + "?" + url.Values{key: {token}}.Encode()
}

func (a *api) ListUsers(ctx context.Context, _ hubapi.ListUsersRequestObject) (hubapi.ListUsersResponseObject, error) {
	users, err := a.server.accounts.Users(ctx)
	if err != nil {
		return nil, err
	}
	list := make([]hubapi.User, 0, len(users))
	for _, u := range users {
		list = append(list, userOf(u))
	}
	logging.FromContext(ctx).Info("users listed", "count", len(list))
	return hubapi.ListUsers200JSONResponse{Users: list}, nil
}

func (a *api) GetUser(ctx context.Context, req hubapi.GetUserRequestObject) (hubapi.GetUserResponseObject, error) {
	u, err := a.user(ctx, req.Username)
	if err != nil {
		return nil, err
	}
	d, err := a.userDetail(ctx, u)
	if err != nil {
		return nil, err
	}
	return hubapi.GetUser200JSONResponse{User: d}, nil
}

func (a *api) UpdateUser(ctx context.Context, req hubapi.UpdateUserRequestObject) (hubapi.UpdateUserResponseObject, error) {
	u, err := a.user(ctx, req.Username)
	if err != nil {
		return nil, err
	}
	accounts := a.server.accounts
	change, result := accounts.Enable, hubapi.ResultEnabled
	if req.Body.Disabled {
		change, result = accounts.Disable, hubapi.ResultDisabled
	}
	changed, err := change(ctx, account.Admin, u.ID)
	if err != nil {
		return nil, &userError{username: req.Username, err: err}
	}
	if !changed {
		logging.FromContext(ctx).Info("user unchanged", "user_id", u.ID, "disabled", req.Body.Disabled)
		result = hubapi.ResultUnchanged
	}
	if req.Body.Disabled {
		// Even unchanged: a node that connected meanwhile is refused anyway.
		a.server.nodes.CutOffUser(ctx, u.ID, node.ReasonDisabled)
	}
	if u, err = a.user(ctx, req.Username); err != nil {
		return nil, err
	}
	d, err := a.userDetail(ctx, u)
	if err != nil {
		return nil, err
	}
	return hubapi.UpdateUser200JSONResponse{Result: result, User: d}, nil
}

func (a *api) CreateUserReset(ctx context.Context, req hubapi.CreateUserResetRequestObject) (hubapi.CreateUserResetResponseObject, error) {
	u, err := a.user(ctx, req.Username)
	if err != nil {
		return nil, err
	}
	secondFactor := req.Body.SecondFactor != nil && *req.Body.SecondFactor
	r, err := a.server.accounts.CreateReset(ctx, account.Admin, u.ID, secondFactor)
	if err != nil {
		return nil, &userError{username: req.Username, err: err}
	}
	return hubapi.CreateUserReset201JSONResponse{
		Result: hubapi.ResultReset,
		Reset:  hubapi.Reset{Link: a.link("/reset", "token", r.Token), ExpiresAt: r.ExpiresAt, SecondFactor: r.SecondFactor},
	}, nil
}

func (a *api) RemoveUser(ctx context.Context, req hubapi.RemoveUserRequestObject) (hubapi.RemoveUserResponseObject, error) {
	u, err := a.user(ctx, req.Username)
	if err != nil {
		return nil, err
	}
	d, err := a.userDetail(ctx, u) // as they were
	if err != nil {
		return nil, err
	}
	if err := a.server.accounts.Remove(ctx, account.Admin, u.ID); err != nil {
		return nil, &userError{username: req.Username, err: err}
	}
	a.server.nodes.CutOffUser(ctx, u.ID, node.ReasonRevoked)
	return hubapi.RemoveUser200JSONResponse{Result: hubapi.ResultRemoved, User: d}, nil
}

func (a *api) VerifyAudit(ctx context.Context, _ hubapi.VerifyAuditRequestObject) (hubapi.VerifyAuditResponseObject, error) {
	v, err := audit.Verify(ctx, a.server.db)
	if err != nil {
		return nil, err
	}
	if v.Broken != nil {
		return nil, &AuditBrokenError{Broken: *v.Broken}
	}
	r := hubapi.Audit{Rows: v.Rows}
	if v.Head != "" {
		r.Head = &v.Head
	}
	return hubapi.VerifyAudit200JSONResponse{Audit: r}, nil
}

// user finds the user named username, whatever its case.
func (a *api) user(ctx context.Context, username string) (account.User, error) {
	log := logging.FromContext(ctx)
	u, err := a.server.accounts.UserByUsername(ctx, username)
	if err != nil {
		log.Info("user not found", "username", username)
		return account.User{}, &userError{username: username, err: err}
	}
	log.Info("user found", "username", username, "user_id", u.ID)
	return u, nil
}

func userOf(u account.User) hubapi.User {
	r := hubapi.User{Username: u.Username, CreatedAt: u.CreatedAt, Disabled: u.Disabled(), Nodes: u.Nodes}
	if !u.LastLoginAt.IsZero() {
		r.LastLoginAt = &u.LastLoginAt
	}
	if u.SecondFactor != "" {
		r.SecondFactor = &u.SecondFactor
	}
	return r
}

// userDetail is u with their nodes.
func (a *api) userDetail(ctx context.Context, u account.User) (hubapi.UserDetail, error) {
	nodes, err := a.server.nodes.Nodes(ctx, u.ID)
	if err != nil {
		return hubapi.UserDetail{}, err
	}
	list := make([]hubapi.Node, 0, len(nodes))
	for _, n := range nodes {
		list = append(list, a.server.nodeOf(n))
	}
	r := userOf(u)
	return hubapi.UserDetail{
		Username: r.Username, CreatedAt: r.CreatedAt, LastLoginAt: r.LastLoginAt,
		Disabled: r.Disabled, SecondFactor: r.SecondFactor, Sessions: u.Sessions,
		Nodes: r.Nodes, NodeList: list,
	}, nil
}
