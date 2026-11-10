package account

import (
	"context"
	"errors"

	"github.com/srjn45/wdx/internal/logging"
)

// ErrBusy is a password not hashed because too many wait to be: the try is
// refused at once, as a rate limit's is.
var ErrBusy = errors.New("too many passwords are being checked at once")

// How many argon2id runs go at once, and how many more may wait for one: each
// holds Argon2's memory, 19 MiB with OWASP's, while it runs, and nothing while
// it waits.
const (
	hashSlotCount = 2
	hashWaiting   = 16
)

// slots is a semaphore with a bounded queue.
type slots struct {
	admitted chan struct{} // running or waiting
	running  chan struct{}
}

func newSlots(running, waiting int) *slots {
	return &slots{admitted: make(chan struct{}, running+waiting), running: make(chan struct{}, running)}
}

// run runs fn once a slot is free, waiting at most until ctx ends; with the
// queue full it fails at once with ErrBusy.
func (s *slots) run(ctx context.Context, fn func()) error {
	select {
	case s.admitted <- struct{}{}:
	default:
		return ErrBusy
	}
	defer func() { <-s.admitted }()
	select {
	case s.running <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.running }()
	fn()
	return nil
}

// hashPassword hashes password in a hash slot.
func (s *Store) hashPassword(ctx context.Context, password string) (hash string, err error) {
	if busy := s.inSlot(ctx, func() { hash, err = s.argon.hash(password) }); busy != nil {
		return "", busy
	}
	return hash, err
}

// passwordMatches checks password against encoded in a hash slot.
func (s *Store) passwordMatches(ctx context.Context, encoded, password string) (ok bool, err error) {
	if busy := s.inSlot(ctx, func() { ok, err = matches(encoded, password) }); busy != nil {
		return false, busy
	}
	return ok, err
}

// inSlot runs hash, an argon2id run, in a hash slot, through the
// AroundHash of the options.
func (s *Store) inSlot(ctx context.Context, hash func()) error {
	err := s.hashSlots.run(ctx, func() { s.aroundHash(hash) })
	if errors.Is(err, ErrBusy) {
		logging.FromContext(ctx).Info("password not checked", "reason", "busy")
	}
	return err
}
