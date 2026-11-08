package clikit

import (
	"errors"

	"github.com/srjn45/wdx/internal/settings"
)

// Exit codes.
const (
	ExitOK       = 0
	ExitRejected = 1 // a conflict, not found or cancelled
	ExitUsage    = 2
	ExitSystem   = 3
)

// The codes every program has, which the framework reports itself.
const (
	CodeUsage    = "usage"
	CodeInternal = "internal"
)

// CodeInfo is what an error code means and how it is reported.
type CodeInfo struct {
	Status      int // HTTP status
	Exit        int // CLI exit code
	Description string
}

// Problem is RFC 9457 Problem Details as every wdx program's API and CLI
// report them; its JSON is api.Problem's, key for key and in the same order.
type Problem struct {
	Code    string          `json:"code"`
	DebugId string          `json:"debug_id"`
	Detail  string          `json:"detail"`
	Details *map[string]any `json:"details"`
	Hint    *string         `json:"hint"`
	Status  int             `json:"status"`
	Title   string          `json:"title"`
	Type    string          `json:"type"`

	// Lines, when set, are the text printed in place of Detail: what failed,
	// then one line for each of its problems.
	Lines []string `json:"-"`
}

// NewProblem returns the problem of code, with the status and title info
// gives it.
func NewProblem(code string, info CodeInfo, detail string) Problem {
	return Problem{
		Type:   "urn:wdx:error:" + code,
		Title:  info.Description,
		Status: info.Status,
		Detail: detail,
		Code:   code,
	}
}

// UsageError is a command line that cannot run as given.
type UsageError struct {
	Err error
}

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

// Hinted adds a hint telling the user what to do about Err.
type Hinted struct {
	Err  error
	Hint string
}

func (e *Hinted) Error() string { return e.Err.Error() }
func (e *Hinted) Unwrap() error { return e.Err }

// problemOf returns err as the problem a command reports: usage, the
// program's own, a settings error, or internal; a Hinted in its chain gives
// the hint.
func problemOf(env Env, err error) Problem {
	var p Problem
	if _, ok := errors.AsType[*UsageError](err); ok {
		p = env.newProblem(CodeUsage, err.Error())
	} else if pp, ok := env.mapProblem(err); ok {
		p = pp
	} else if e, ok := errors.AsType[*settings.Error](err); ok {
		p = settingsProblem(env, e)
	} else {
		p = env.newProblem(CodeInternal, err.Error())
	}
	if h, ok := errors.AsType[*Hinted](err); ok {
		p.Hint = &h.Hint
	}
	return p
}

// settingsProblem is the problem of settings that failed to load or to be
// written anew.
func settingsProblem(env Env, e *settings.Error) Problem {
	p := env.newProblem(CodeUsage, e.Error())
	hint := "fix or remove them"
	if e.OnlyUnknown() {
		hint = "run " + env.Program.Name + " setting write, which comments them out at the end of the file"
	}
	p.Hint = &hint
	p.Details = &map[string]any{"settings": e.Problems}
	return p
}

func (env Env) mapProblem(err error) (Problem, bool) {
	if env.Problem == nil {
		return Problem{}, false
	}
	return env.Problem(err)
}
