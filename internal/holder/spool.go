package holder

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// spool is a shell's output on disk, from the shell's start: the PTY's
// reader appends to it without waiting for anyone, and the vt and the
// clients attaching read it at their own pace (docs/ATTACH.md). Positions
// count the bytes since the shell started; the files are segments named by
// the position they start at.
type spool struct {
	dir string
	// segment is the size of a segment file. keep segments the vt has
	// consumed stay for clients that read after a snapshot; at most backlog
	// bytes the vt has not consumed are kept, beyond which the oldest go.
	segment, backlog int64
	keep             int

	mu       sync.Mutex
	starts   []int64            // the segments' start positions, oldest first
	files    map[int64]*os.File // by start; the last one is written
	end      int64
	consumed int64
	changed  chan struct{}
	closed   bool
}

const (
	spoolSegment = 8 << 20
	spoolKeep    = 2
	spoolBacklog = 256 << 20
)

// openSpool starts an empty spool in dir: a spool belongs to one run of a
// shell, so what a previous run left is deleted.
func openSpool(dir string, segment, backlog int64, keep int) (*spool, error) {
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &spool{dir: dir, segment: segment, backlog: backlog, keep: keep, files: map[int64]*os.File{}, changed: make(chan struct{})}
	if err := s.create(0); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *spool) create(start int64) error {
	f, err := os.OpenFile(filepath.Join(s.dir, fmt.Sprintf("%016d", start)), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	s.starts = append(s.starts, start)
	s.files[start] = f
	return nil
}

// append adds output at the end, opening segments as they fill.
func (s *spool) append(p []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return os.ErrClosed // finished
	}
	if len(p) > 0 {
		defer s.wake()
	}
	for len(p) > 0 {
		last := s.starts[len(s.starts)-1]
		room := last + s.segment - s.end
		if room == 0 {
			if err := s.create(s.end); err != nil {
				return err
			}
			continue
		}
		n := int(min(room, int64(len(p))))
		if _, err := s.files[last].Write(p[:n]); err != nil {
			return err
		}
		s.end += int64(n)
		p = p[n:]
	}
	s.drop()
	return nil
}

// consume records that the vt has read up to pos: the segments before it
// can go, but for the last keep.
func (s *spool) consume(pos int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consumed = max(s.consumed, pos)
	s.drop()
}

// drop deletes the oldest segments that are consumed and not among the
// last keep, and those beyond the backlog; s.mu is held.
func (s *spool) drop() {
	for len(s.starts) > 1 {
		consumed := s.starts[1] <= s.consumed && len(s.starts) > s.keep
		if !consumed && s.end-s.starts[1] < s.backlog {
			return
		}
		start := s.starts[0]
		s.files[start].Close()
		delete(s.files, start)
		// The spool goes on without the file: a reader skips past it.
		_ = os.Remove(filepath.Join(s.dir, fmt.Sprintf("%016d", start)))
		s.starts = s.starts[1:]
	}
}

// read copies output from position pos into p. from is where the bytes
// start: pos, or the oldest position kept when the output at pos was
// dropped. n is 0 at the end, and err is io.EOF there once the spool is
// finished.
func (s *spool) read(pos int64, p []byte) (from int64, n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	from = max(pos, s.starts[0])
	if from >= s.end {
		if s.closed {
			return from, 0, io.EOF
		}
		return from, 0, nil
	}
	i := len(s.starts) - 1
	for s.starts[i] > from {
		i--
	}
	start := s.starts[i]
	n = int(min(int64(len(p)), start+s.segment-from, s.end-from))
	n, err = s.files[start].ReadAt(p[:n], from-start)
	return from, n, err
}

// position is the end of the output.
func (s *spool) position() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.end
}

// wait returns a channel closed by the next append, or by finish.
func (s *spool) wait() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed
}

// wake tells the readers waiting there is more; s.mu is held.
func (s *spool) wake() {
	close(s.changed)
	s.changed = make(chan struct{})
}

// finish marks the end of the output: readers read what is left, then
// io.EOF.
func (s *spool) finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.wake()
	}
}

// close finishes the spool and closes its files; nothing reads it after.
// The files stay until the next run's openSpool.
func (s *spool) close() {
	s.finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.files {
		f.Close()
	}
}
