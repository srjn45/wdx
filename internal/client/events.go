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
	Removed bool
}

// maxEvent is the largest event a stream reads; a project with every
// terminal it can hold stays far below it.
const maxEvent = 16 << 20

// ProjectEvents follows the project ref names, until ctx ends or Close.
// A project that does not exist fails here, as a *ProblemError.
func (c *Client) ProjectEvents(ctx context.Context, ref string) (*ProjectStream, error) {
	resp, err := c.api.StreamProject(ctx, ref)
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
	var event string
	var data strings.Builder
	for s.scanner.Scan() {
		line := s.scanner.Text()
		switch {
		case line == "" && event != "":
			return decodeEvent(event, data.String())
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
		// Comments and fields this stream does not use are skipped.
	}
	if err := s.scanner.Err(); err != nil {
		return ProjectEvent{}, err
	}
	return ProjectEvent{}, io.EOF
}

func decodeEvent(event, data string) (ProjectEvent, error) {
	switch event {
	case "project":
		var v api.ProjectEnvelope
		if err := json.Unmarshal([]byte(data), &v); err != nil {
			return ProjectEvent{}, fmt.Errorf("project event unreadable: %w", err)
		}
		return ProjectEvent{Project: v.Project}, nil
	case "removed":
		return ProjectEvent{Removed: true}, nil
	}
	return ProjectEvent{}, fmt.Errorf("unexpected event %q from the daemon", event)
}

// Close stops following the project.
func (s *ProjectStream) Close() error { return s.body.Close() }
