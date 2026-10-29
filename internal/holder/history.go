package holder

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// A terminal's history is the lines that scrolled off its screen, numbered
// from 0 for the terminal's life, across relaunches of its shell. They are
// kept in segment files of historySegment lines named by their first line's
// number; a line is its rendered text, styles and links included, ending in
// a newline (docs/ATTACH.md).
const historySegment = 10_000

// historyWriter appends lines to a terminal's history; the holder is its
// only writer.
type historyWriter struct {
	dir string
	// limit bounds the lines kept: the oldest segment goes once more are
	// kept, so between limit−historySegment and limit stay.
	limit  int64
	starts []int64 // the segments' first lines, oldest first
	f      *os.File
	next   int64
}

// openHistory continues the history in dir, after its last complete line.
func openHistory(dir string, limit int64) (*historyWriter, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	starts, err := historySegments(dir)
	if err != nil {
		return nil, err
	}
	h := &historyWriter{dir: dir, limit: limit, starts: starts}
	if len(starts) == 0 {
		return h, h.create(0)
	}
	last := starts[len(starts)-1]
	f, err := os.OpenFile(segmentPath(dir, last), os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	lines, size, err := completeLines(f)
	if err == nil {
		// A line the last writer did not finish is dropped.
		err = f.Truncate(size)
	}
	if err == nil {
		_, err = f.Seek(size, io.SeekStart)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	h.f, h.next = f, last+lines
	return h, nil
}

func (h *historyWriter) create(start int64) error {
	f, err := os.OpenFile(segmentPath(h.dir, start), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	h.f = f
	h.starts = append(h.starts, start)
	return nil
}

// append adds lines at the end, starting segments as they fill and
// deleting the oldest past the limit.
func (h *historyWriter) append(lines ...string) error {
	var buf bytes.Buffer
	for len(lines) > 0 {
		start := h.starts[len(h.starts)-1]
		if h.next == start+historySegment {
			if err := h.f.Close(); err != nil {
				return err
			}
			if err := h.create(h.next); err != nil {
				return err
			}
			continue
		}
		n := min(len(lines), int(start+historySegment-h.next))
		buf.Reset()
		for _, line := range lines[:n] {
			buf.WriteString(strings.ReplaceAll(line, "\n", " "))
			buf.WriteByte('\n')
		}
		if _, err := h.f.Write(buf.Bytes()); err != nil {
			return err
		}
		h.next += int64(n)
		lines = lines[n:]
	}
	return h.trim()
}

func (h *historyWriter) trim() error {
	for len(h.starts) > 1 && h.next-h.starts[0] > h.limit {
		if err := os.Remove(segmentPath(h.dir, h.starts[0])); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		h.starts = h.starts[1:]
	}
	return nil
}

// close closes the segment being written.
func (h *historyWriter) close() error {
	return h.f.Close()
}

// History is a run of lines from a terminal's history.
type History struct {
	From  int64 // the number of the first line in Lines
	Next  int64 // the number the next line written will take
	First int64 // the number of the oldest line kept
	Lines []string
}

// ReadHistory reads up to count lines of the history in dir from line
// from on; a from before the oldest line kept starts at that one. The
// holder may be writing it meanwhile. No history yet is an empty one.
func ReadHistory(dir string, from int64, count int) (History, error) {
	starts, next, err := scanHistory(dir)
	if err != nil || len(starts) == 0 {
		return History{}, err
	}
	h := History{From: max(from, starts[0]), Next: next, First: starts[0]}
	h.From = min(h.From, next)
	for i, start := range starts {
		if len(h.Lines) == count || h.From+int64(len(h.Lines)) >= next {
			break
		}
		at := h.From + int64(len(h.Lines))
		if i+1 < len(starts) && starts[i+1] <= at {
			continue
		}
		lines, err := readSegment(dir, start, at-start, count-len(h.Lines))
		if errors.Is(err, os.ErrNotExist) && len(h.Lines) == 0 {
			// Trimmed since the scan: start at the next segment.
			h.From, h.First = start+historySegment, start+historySegment
			continue
		}
		if err != nil {
			return History{}, err
		}
		h.Lines = append(h.Lines, lines...)
	}
	return h, nil
}

// TailHistory reads the last count lines of the history in dir.
func TailHistory(dir string, count int) (History, error) {
	_, next, err := scanHistory(dir)
	if err != nil {
		return History{}, err
	}
	return ReadHistory(dir, next-int64(count), count)
}

// scanHistory lists the segments in dir and counts the lines: the next
// line's number.
func scanHistory(dir string) (starts []int64, next int64, err error) {
	starts, err = historySegments(dir)
	if errors.Is(err, os.ErrNotExist) || err == nil && len(starts) == 0 {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	last := starts[len(starts)-1]
	f, err := os.Open(segmentPath(dir, last))
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	lines, _, err := completeLines(f)
	if err != nil {
		return nil, 0, err
	}
	return starts, last + lines, nil
}

// readSegment reads up to count complete lines from segment start, after
// skipping skip lines.
func readSegment(dir string, start, skip int64, count int) ([]string, error) {
	f, err := os.Open(segmentPath(dir, start))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var lines []string
	for count > 0 {
		line, err := r.ReadString('\n')
		if err == io.EOF {
			return lines, nil // a line being written is not complete
		}
		if err != nil {
			return nil, err
		}
		if skip > 0 {
			skip--
			continue
		}
		lines = append(lines, line[:len(line)-1])
		count--
	}
	return lines, nil
}

// completeLines counts the lines in r that end in a newline, and their
// size.
func completeLines(r io.Reader) (lines, size int64, err error) {
	buf := make([]byte, 64<<10)
	var read int64
	for {
		n, err := r.Read(buf)
		for i, b := range buf[:n] {
			if b == '\n' {
				lines++
				size = read + int64(i) + 1
			}
		}
		read += int64(n)
		if err == io.EOF {
			return lines, size, nil
		}
		if err != nil {
			return 0, 0, err
		}
	}
}

func historySegments(dir string) ([]int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var starts []int64
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".log")
		if !ok {
			continue
		}
		if start, err := strconv.ParseInt(name, 10, 64); err == nil {
			starts = append(starts, start)
		}
	}
	slices.Sort(starts)
	return starts, nil
}

func segmentPath(dir string, start int64) string {
	return filepath.Join(dir, fmt.Sprintf("%010d.log", start))
}
