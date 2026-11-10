package server

import (
	"context"
	"errors"

	"github.com/srjn45/wdx/internal/hub/docsync"
	"github.com/srjn45/wdx/internal/hubapi"
)

// maxDocChangesBody is the most a push of doc changes may send; a body over
// it is refused whole, before it is read further.
const maxDocChangesBody = 8 << 20

func (a *api) PushDocChanges(ctx context.Context, req hubapi.PushDocChangesRequestObject) (hubapi.PushDocChangesResponseObject, error) {
	this := caller(ctx)
	changes := make([]docsync.Change, 0, len(req.Body.Changes))
	for _, c := range req.Body.Changes {
		changes = append(changes, changeOf(c))
	}
	results, err := a.server.docs.Apply(ctx, docsync.By{UserID: this.UserID, NodeID: this.ID, Address: address(ctx)}, changes)
	if err != nil {
		return nil, err
	}
	r := hubapi.PushDocChanges200JSONResponse{Results: make([]hubapi.DocChangeResult, 0, len(results))}
	for _, res := range results {
		out := hubapi.DocChangeResult{Seq: res.Seq, Status: hubapi.DocChangeResultStatusApplied}
		if res.Err != nil {
			code, _ := classify(res.Err)
			out.Status, out.Code = hubapi.DocChangeResultStatusRefused, &code
		}
		r.Results = append(r.Results, out)
	}
	return r, nil
}

func changeOf(c hubapi.DocChange) docsync.Change {
	d := docsync.Change{Seq: c.Seq, DocID: c.DocId, Type: docsync.Type(c.Change), Revision: deref(c.Revision)}
	if c.Content != nil {
		d.Content = *c.Content
	}
	d.ContentSHA256 = deref(c.ContentSha256)
	m := &d.Metadata
	m.RepoKey, m.Name, m.Folder, m.Format, m.Status = deref(c.RepoKey), deref(c.Name), deref(c.Folder), deref(c.Format), deref(c.Status)
	m.Kind = string(deref(c.Kind))
	m.Labels = deref(c.Labels)
	m.CreatedAt, m.UpdatedAt = deref(c.CreatedAt), deref(c.UpdatedAt)
	return d
}

// docCodes are the codes of the doc store's refusals.
var docCodes = map[error]hubapi.ErrorCode{
	docsync.ErrNotFound:         hubapi.ErrorCodeNotFound,
	docsync.ErrRevisionGap:      hubapi.ErrorCodeRevisionGap,
	docsync.ErrTooLarge:         hubapi.ErrorCodeDocTooLarge,
	docsync.ErrNameTaken:        hubapi.ErrorCodeDocNameTaken,
	docsync.ErrRevisionConflict: hubapi.ErrorCodeRevisionConflict,
}

// docCode is the code of the doc store's refusal err, and the values it
// involves.
func docCode(err error) (hubapi.ErrorCode, map[string]any, bool) {
	if e, ok := errors.AsType[*docsync.InvalidError](err); ok {
		return hubapi.ErrorCodeInvalidArgument, map[string]any{"argument": e.Field}, true
	}
	for e, code := range docCodes {
		if errors.Is(err, e) {
			return code, nil, true
		}
	}
	return "", nil, false
}
