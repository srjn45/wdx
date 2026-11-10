package hubcli

import (
	"errors"
	"io"
	"sync"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hub/server"
	"github.com/srjn45/wdx/internal/hubapi"
)

// problem maps the errors of wdx-hub's commands to their problems: the hub's,
// a hub that does not answer, and a hub already serving the home.
func problem(err error) (clikit.Problem, bool) {
	if e, ok := errors.AsType[*problemError](err); ok {
		return toProblem(e.problem), true
	}
	if p, ok := updateProblem(err); ok {
		return toProblem(p), true
	}
	if _, ok := errors.AsType[*server.AlreadyRunningError](err); ok {
		p := hubapi.NewProblem(hubapi.ErrorCodeAlreadyRunning, err.Error())
		hint := "stop the hub serving this home first, or give this one another " + hubconfig.EnvPrefix + "HOME"
		p.Hint = &hint
		return toProblem(p), true
	}
	if e, ok := errors.AsType[*server.CAMissingError](err); ok {
		p := hubapi.NewProblem(hubapi.ErrorCodeCaMissing, err.Error())
		hint := "restore the certificate authority to " + e.Dir + ", from the <path>.ca/ directory beside the backup of the database: a new one would log every node out"
		p.Hint = &hint
		return toProblem(p), true
	}
	if unavailable(err) {
		p := hubapi.NewProblem(hubapi.ErrorCodeHubUnavailable, err.Error())
		hint := "run " + name + " serve"
		p.Hint = &hint
		return toProblem(p), true
	}
	return clikit.Problem{}, false
}

// toProblem is p as the framework reports it.
func toProblem(p hubapi.Problem) clikit.Problem {
	return clikit.Problem{
		Code: string(p.Code), DebugId: p.DebugId, Detail: p.Detail, Details: p.Details,
		Hint: p.Hint, Status: p.Status, Title: p.Title, Type: p.Type,
	}
}

// lockedWriter writes one line at a time from the goroutines that share it:
// serve's lines and the hub's, on stdout.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
