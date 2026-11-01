package daemon

import (
	"context"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/terminal"
)

func (s *server) ListMessages(ctx context.Context, req api.ListMessagesRequestObject) (api.ListMessagesResponseObject, error) {
	p, _, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	list, err := s.terminals.Store().Undelivered(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	views := make([]api.Message, 0, len(list))
	for _, m := range list {
		views = append(views, messageView(m))
	}
	return api.ListMessages200JSONResponse{Messages: views}, nil
}

func (s *server) ResendMessage(ctx context.Context, req api.ResendMessageRequestObject) (api.ResendMessageResponseObject, error) {
	m, err := s.terminals.ResendMessage(ctx, req.Message)
	if err != nil {
		return nil, err
	}
	return api.ResendMessage200JSONResponse{Result: api.ResultQueued, Message: messageView(m)}, nil
}

func (s *server) DiscardMessage(ctx context.Context, req api.DiscardMessageRequestObject) (api.DiscardMessageResponseObject, error) {
	if err := s.terminals.Store().DiscardMessage(ctx, req.Message); err != nil {
		return nil, err
	}
	return api.DiscardMessage200JSONResponse{Result: api.ResultRemoved}, nil
}

func messageView(m terminal.Message) api.Message {
	return api.Message{
		Id: m.ID, TerminalId: m.TerminalID, Name: m.Name, Text: m.Text, Instant: m.Instant,
		State: api.MessageState(m.State), Attempts: m.Attempts, CreatedAt: m.CreatedAt,
	}
}
