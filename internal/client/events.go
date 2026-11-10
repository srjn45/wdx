package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/srjn45/wdx/internal/api"
)

// ProjectStream is a project followed with streamProject: every event is
// the project whole.
type ProjectStream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
}

// ProjectEvent is one event of a ProjectStream: the project, or that it
// was removed.
type ProjectEvent struct {
	Project api.Project
	// Tabs are the tabs tide has open in the project; nil from a daemon too old
	// to keep them.
	Tabs []api.Tab
	// TabsRevision is how many changes the tabs had when the event was sent.
	TabsRevision int64
	Removed      bool
}

// maxEvent is the largest event a stream reads; a project with every
// terminal it can hold stays far below it.
const maxEvent = 16 << 20

// ProjectEvents follows the project ref names, until ctx ends or Close.
// A project that does not exist fails here, as a *ProblemError.
func (c *Client) ProjectEvents(ctx context.Context, ref string) (*ProjectStream, error) {
	return c.follow(ctx, ref, nil)
}

// ProjectEventsWithWorktrees is ProjectEvents with the worktrees' statuses in
// the events, which the daemon reads only for as long as this stream stays.
func (c *Client) ProjectEventsWithWorktrees(ctx context.Context, ref string) (*ProjectStream, error) {
	on := true
	return c.follow(ctx, ref, &api.StreamProjectParams{Worktrees: &on})
}

func (c *Client) follow(ctx context.Context, ref string, params *api.StreamProjectParams) (*ProjectStream, error) {
	resp, err := c.api.StreamProject(ctx, ref, params)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		r, err := api.ParseStreamProjectResponse(resp)
		if err != nil {
			return nil, err
		}
		_, err = result[struct{}](resp, r.Body, r.ApplicationproblemJSONDefault)
		return nil, err
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64<<10), maxEvent)
	return &ProjectStream{body: resp.Body, scanner: scanner}, nil
}

// Next waits for the next event. It returns io.EOF once the stream has
// ended: after a removed event, or when the daemon ended it.
func (s *ProjectStream) Next() (ProjectEvent, error) {
	event, data, err := nextEvent(s.scanner)
	if err != nil {
		return ProjectEvent{}, err
	}
	return decodeEvent(event, data)
}

// nextEvent reads a stream's next server-sent event: its name and data. It
// returns io.EOF once the stream has ended.
func nextEvent(scanner *bufio.Scanner) (event, data string, err error) {
	var b strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "" && event != "":
			return event, b.String(), nil
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
		// Comments and fields this stream does not use are skipped.
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	return "", "", io.EOF
}

func decodeEvent(event, data string) (ProjectEvent, error) {
	switch event {
	case "project":
		var v api.ProjectEnvelope
		if err := json.Unmarshal([]byte(data), &v); err != nil {
			return ProjectEvent{}, fmt.Errorf("project event unreadable: %w", err)
		}
		return ProjectEvent{Project: v.Project, Tabs: v.Tabs, TabsRevision: v.TabsRevision}, nil
	case "removed":
		return ProjectEvent{Removed: true}, nil
	}
	return ProjectEvent{}, fmt.Errorf("unexpected event %q from the daemon", event)
}

// Close stops following the project.
func (s *ProjectStream) Close() error { return s.body.Close() }

// HubSessionStream is the sessions into this node followed with
// streamHubSessions: every event is all of them.
type HubSessionStream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
}

// HubSessionEvents follows the sessions into this node, until ctx ends or
// Close.
func (c *Client) HubSessionEvents(ctx context.Context) (*HubSessionStream, error) {
	resp, err := c.api.StreamHubSessions(ctx)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		r, err := api.ParseStreamHubSessionsResponse(resp)
		if err != nil {
			return nil, err
		}
		_, err = result[struct{}](resp, r.Body, r.ApplicationproblemJSONDefault)
		return nil, err
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 4<<10), maxEvent)
	return &HubSessionStream{body: resp.Body, scanner: scanner}, nil
}

// Next waits for the sessions into this node and the wrong codes as they
// are next. It returns io.EOF once the daemon ended the stream.
func (s *HubSessionStream) Next() (api.HubSessionEvent, error) {
	event, data, err := nextEvent(s.scanner)
	if err != nil {
		return api.HubSessionEvent{}, err
	}
	if event != "sessions" {
		return api.HubSessionEvent{}, fmt.Errorf("unexpected event %q from the daemon", event)
	}
	var v api.HubSessionEvent
	if err := json.Unmarshal([]byte(data), &v); err != nil {
		return api.HubSessionEvent{}, fmt.Errorf("sessions event unreadable: %w", err)
	}
	return v, nil
}

// Close stops following the sessions.
func (s *HubSessionStream) Close() error { return s.body.Close() }
