package hublink

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/logging"
)

// The size of a push, the wdx doc HUB_DOCS_PUSH's: at most pushChanges
// changes, in a body of at most pushBytes, which the hub refuses whole above.
const (
	pushChanges = 100
	pushBytes   = 8 << 20
)

// Outbox is the changes of synced projects' docs waiting for the hub, which
// the daemon keeps and the link sends.
type Outbox interface {
	// Next returns up to limit of the oldest changes waiting, in order.
	Next(ctx context.Context, limit int) ([]hubapi.DocChange, error)
	// Done deletes the changes with seqs, which the hub applied at at.
	Done(ctx context.Context, seqs []int64, at time.Time) error
	// Fail marks the change with seq refused for good with code, which holds
	// up its doc's later changes until a new one of it.
	Fail(ctx context.Context, seq int64, code string) error
	// Clear deletes every change waiting, at a logout.
	Clear(ctx context.Context) error
	// EnqueueAll queues every doc of every synced project anew, at a login,
	// in place of what was waiting.
	EnqueueAll(ctx context.Context) error
}

// refusedForGood are the codes the hub refuses a change with that are its
// doc's own: sending it again changes nothing until the doc changes.
// not_found is a doc whose id is another user's on the hub.
var refusedForGood = map[hubapi.ErrorCode]bool{
	hubapi.ErrorCodeNotFound:     true,
	hubapi.ErrorCodeDocNameTaken: true,
	hubapi.ErrorCodeDocTooLarge:  true,
	hubapi.ErrorCodeRevisionGap:  true,
}

// Kick tells the link the outbox has changes, which it pushes when the
// control connection is up; it never waits.
func (l *Link) Kick() {
	select {
	case l.kicked <- struct{}{}:
	default:
	}
}

// keepPushing pushes the outbox while ctx, a control connection welcomed,
// lasts: at once, then each time it is kicked, and after a failure once
// the control connection's backoff has passed.
func (l *Link) keepPushing(ctx context.Context, c *credentials, client *http.Client) {
	if l.opts.Outbox == nil {
		return
	}
	nodes, err := hubapi.NewClientWithResponses(c.NodesURL, hubapi.WithHTTPClient(client), hubapi.WithRequestEditorFn(versionHeader))
	if err != nil {
		logging.FromContext(ctx).Error("docs push not started", "err", err)
		return
	}
	backoff := l.opts.MinBackoff
	for {
		err := l.pushAll(ctx, nodes, c.NodesURL)
		if ctx.Err() != nil {
			return
		}
		var wait <-chan time.Time
		if err != nil {
			delay := jitter(backoff)
			logging.FromContext(ctx).Info("docs push failed", "err", err, "retry_in_ms", delay.Milliseconds())
			wait = l.opts.Clock.After(delay)
			backoff = min(backoff*2, l.opts.MaxBackoff)
		} else {
			backoff = l.opts.MinBackoff
		}
		if wait != nil {
			select {
			case <-ctx.Done():
				return
			case <-wait:
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-l.kicked:
		}
	}
}

// pushAll pushes batch after batch until the outbox has nothing the hub can
// be sent, or a batch fails.
func (l *Link) pushAll(ctx context.Context, nodes *hubapi.ClientWithResponses, url string) error {
	for {
		changes, err := l.opts.Outbox.Next(ctx, pushChanges)
		if err != nil {
			return fmt.Errorf("outbox: %w", err)
		}
		if len(changes) == 0 {
			return nil
		}
		if err := l.push(ctx, nodes, url, batch(changes)); err != nil {
			return err
		}
	}
}

// batch is the first of changes that fit in a push, at least one.
func batch(changes []hubapi.DocChange) []hubapi.DocChange {
	size := len(`{"changes":[]}`)
	for i, c := range changes {
		b, err := json.Marshal(c)
		if err != nil {
			return changes[:max(i, 1)]
		}
		size += len(b) + 1
		if size > pushBytes && i > 0 {
			return changes[:i]
		}
	}
	return changes
}

// push sends changes to the hub and records its answer to each: applied
// ones done, ones refused for good failed. Any other answer leaves the rest
// to be sent again, and is an error once the others are recorded.
func (l *Link) push(ctx context.Context, nodes *hubapi.ClientWithResponses, url string, changes []hubapi.DocChange) error {
	log := logging.FromContext(ctx)
	r, err := nodes.PushDocChangesWithResponse(ctx, hubapi.DocChanges{Changes: changes})
	if err != nil {
		return &UnreachableError{URL: url, Err: err}
	}
	if r.JSON200 == nil {
		return refused(url, r.StatusCode(), r.ApplicationproblemJSONDefault)
	}
	sent := make(map[int64]hubapi.DocChange, len(changes))
	for _, c := range changes {
		sent[c.Seq] = c
	}
	var applied []int64
	held := map[string]bool{} // the docs refused for good
	var failed, left int
	var leftCode hubapi.ErrorCode
	for _, res := range r.JSON200.Results {
		c, ok := sent[res.Seq]
		if !ok {
			continue
		}
		delete(sent, res.Seq)
		if res.Status == hubapi.DocChangeResultStatusApplied {
			applied = append(applied, res.Seq)
			continue
		}
		if held[c.DocId] {
			// Refused as it follows one refused: left, as the outbox holds
			// it up with the other.
			continue
		}
		var code hubapi.ErrorCode
		if res.Code != nil {
			code = *res.Code
		}
		if !refusedForGood[code] {
			left++
			leftCode = code
			continue
		}
		log.Info("doc push refused", "doc_id", c.DocId, "name", deref(c.Name), "change", string(c.Change), "revision", deref(c.Revision), "code", string(code))
		if err := l.opts.Outbox.Fail(ctx, res.Seq, string(code)); err != nil {
			return fmt.Errorf("outbox: %w", err)
		}
		held[c.DocId] = true
		failed++
	}
	left += len(sent)
	// Logged before the outbox changes, so that whoever sees it change finds
	// the line saying what the hub answered.
	log.Info("docs pushed", "sent", len(changes), "applied", len(applied), "refused", failed, "left", left)
	if err := l.opts.Outbox.Done(ctx, applied, l.opts.Clock.Now()); err != nil {
		return fmt.Errorf("outbox: %w", err)
	}
	if left > 0 {
		return fmt.Errorf("the hub applied %d of the changes sent, %d refused, the last with %q, or not answered", len(applied)+failed, left, leftCode)
	}
	return nil
}

func deref[T any](p *T) T {
	var v T
	if p != nil {
		v = *p
	}
	return v
}

// enqueueAll queues every synced doc anew for the hub just logged in to,
// which may have none of them.
func (l *Link) enqueueAll(ctx context.Context) {
	if l.opts.Outbox == nil {
		return
	}
	if err := l.opts.Outbox.EnqueueAll(ctx); err != nil {
		logging.FromContext(ctx).Error("docs not queued for the hub", "err", err)
	}
}

// clearOutbox deletes what was waiting for the hub logged out of, which
// must never reach another; a login's EnqueueAll replaces what is left.
func (l *Link) clearOutbox(ctx context.Context) {
	if l.opts.Outbox == nil {
		return
	}
	if err := l.opts.Outbox.Clear(ctx); err != nil {
		logging.FromContext(ctx).Error("hub outbox not cleared", "err", err)
	}
}
