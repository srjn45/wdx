package aca

import (
	"context"
	"errors"
)

// ErrNotSupported is what an AI coding agent answers for what it cannot do yet.
var ErrNotSupported = errors.New("this AI coding agent cannot do that yet")

// The types below stand in for the methods an AI coding agent has not written:
// it embeds each it needs and drops it once it writes the method. docs/ACA.md.

// NoBackUp is BackUp for an AI coding agent that keeps no backup yet.
type NoBackUp struct{}

// BackUp implements AiCodingAgent.
func (NoBackUp) BackUp(context.Context, Launch) (Backup, error) {
	return Backup{}, ErrNotSupported
}

// NoBeginSession is BeginSession for an AI coding agent that cannot be restarted
// in a new session yet.
type NoBeginSession struct{}

// BeginSession implements AiCodingAgent.
func (NoBeginSession) BeginSession(context.Context, Launch, string) error {
	return ErrNotSupported
}

func (NoBeginSession) beginsNoSession() {}

// NoBrief is Brief for an AI coding agent with no brief yet.
type NoBrief struct{}

// Brief implements AiCodingAgent.
func (NoBrief) Brief(context.Context, Launch, int) (Brief, error) {
	return Brief{}, ErrNotSupported
}

// NoDetached is Detached and StopDetached for an AI coding agent that runs
// nothing outside its terminal that wdx knows of.
type NoDetached struct{}

// Detached implements AiCodingAgent: nothing.
func (NoDetached) Detached(context.Context, Launch) ([]DetachedSession, error) {
	return nil, nil
}

// StopDetached implements AiCodingAgent; Detached finds nothing to stop.
func (NoDetached) StopDetached(context.Context, Launch, DetachedSession) error {
	return ErrNotSupported
}

// CanBeginSession says whether a can begin a new session, which a restart must
// know before it stops anything: false while it embeds NoBeginSession.
func CanBeginSession(a AiCodingAgent) bool {
	_, stub := a.(interface{ beginsNoSession() })
	return !stub
}
