package aca

import (
	"context"
	"errors"
	"io"
)

// ErrNotSupported is what an AI coding agent answers for what it cannot do yet.
var ErrNotSupported = errors.New("this AI coding agent cannot do that yet")

// ErrNoContextSize is what ContextSize answers before an AI coding agent has told
// how full its context is.
var ErrNoContextSize = errors.New("no context size told yet")

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

// NoContextSize is ContextSize for an AI coding agent whose context size wdx does
// not read yet.
type NoContextSize struct{}

// ContextSize implements AiCodingAgent.
func (NoContextSize) ContextSize(Launch) (ContextSize, error) {
	return ContextSize{}, ErrNotSupported
}

// NoSteer is Steer for an AI coding agent that cannot take a message into a running
// turn: an instant message stops the turn with Interrupt, then goes in with Deliver.
type NoSteer struct{}

// Steer implements AiCodingAgent.
func (NoSteer) Steer(io.Writer, string) error { return ErrNotSupported }

func (NoSteer) steersNot() {}

// CanSteer says whether a can take a message into a running turn, which the holder
// must know before it presses the interrupt: false while it embeds NoSteer.
func CanSteer(a AiCodingAgent) bool {
	_, stub := a.(interface{ steersNot() })
	return !stub
}

// CanBeginSession says whether a can begin a new session, which a restart must
// know before it stops anything: false while it embeds NoBeginSession.
func CanBeginSession(a AiCodingAgent) bool {
	_, stub := a.(interface{ beginsNoSession() })
	return !stub
}
