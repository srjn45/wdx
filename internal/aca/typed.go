package aca

import (
	"io"
	"time"
)

// typedSettle is the wait between two steps, a paste settling, as the holder
// waited before it read the screen for each.
const typedSettle = 150 * time.Millisecond

// Typed is how the holder types a message until it runs the steps itself, reading
// the screen for when (the wdx doc SEND_CONFIRMED): Paste, Submit and, to steer,
// SendNow, with typedSettle before each key, the keys and timing the ACAs had
// before the steps. steer is nil for an ACA that cannot steer.
func Typed(a AiCodingAgent) (deliver, steer func(w io.Writer, message string) error) {
	settle := func() { time.Sleep(typedSettle) }
	deliver = func(w io.Writer, message string) error {
		if err := a.Paste(w, message); err != nil {
			return err
		}
		settle()
		return a.Submit(w, settle)
	}
	if !CanSteer(a) {
		return deliver, nil
	}
	steer = func(w io.Writer, message string) error {
		if err := deliver(w, message); err != nil {
			return err
		}
		settle()
		return a.SendNow(w)
	}
	return deliver, steer
}
