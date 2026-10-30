package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

func TestProjectEvents(t *testing.T) {
	t.Parallel()
	socket := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/api/events" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(api.Problem{Code: api.ErrorCodeNotFound, Status: 404, Detail: "project not found"})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, n := range []int{0, 2} {
			b, _ := json.Marshal(api.ProjectEnvelope{Project: api.Project{Name: "api", Terminals: &[]api.Terminal{}, Worktrees: make([]api.Worktree, n)}})
			fmt.Fprintf(w, ": a comment\nevent: project\ndata: %s\n\n", b)
		}
		fmt.Fprint(w, "event: removed\ndata: {\"project_id\":\"proj-1\"}\n\n")
	})
	c := New(Options{Socket: socket})

	s, err := c.ProjectEvents(context.Background(), "api")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, want := range []int{0, 2} {
		e, err := s.Next()
		if err != nil || e.Removed || e.Project.Name != "api" || len(e.Project.Worktrees) != want {
			t.Fatalf("event: %+v, %v", e, err)
		}
	}
	if e, err := s.Next(); err != nil || !e.Removed {
		t.Fatalf("want removed, got %+v, %v", e, err)
	}
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("want io.EOF after the end, got %v", err)
	}

	_, err = c.ProjectEvents(context.Background(), "nope")
	if p, ok := errors.AsType[*ProblemError](err); !ok || p.Problem.Code != api.ErrorCodeNotFound {
		t.Fatalf("want not_found, got %v", err)
	}
}
