package holder

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Version is the protocol version this binary speaks. A holder keeps
// running the binary it was started from, so a daemon must speak the
// previous version too.
const Version = 1

// drives reports whether this daemon can drive a holder speaking version v.
// Version 2 adds attaching clients and leaves the daemon's frames as they
// are, so a daemon rolled back from the release that brings it keeps the
// terminals that release started; docs/ATTACH.md.
func drives(v int) bool {
	return v == 1 || v == 2
}

// A frame is one byte of type, the payload's length as 4 bytes big-endian,
// and the payload: JSON for control frames.
type frameType byte

const (
	frameHello   frameType = 1 // both ways, first: the daemon's version, then the holder's Hello
	frameExited  frameType = 2 // holder to daemon: the shell ended, with an Exit
	frameStop    frameType = 3 // daemon to holder: stop the shell, answered by frameExited
	frameRelease frameType = 4 // daemon to holder: the exit is recorded, quit
)

// maxPayload bounds a frame read, so a broken peer cannot make the reader
// allocate without end.
const maxPayload = 1 << 20

// Hello is what a holder tells the daemon that connects.
type Hello struct {
	Version    int    `json:"version"`
	TerminalID string `json:"terminal_id,omitempty"`
	PID        int    `json:"pid,omitempty"` // the shell's
	HolderPID  int    `json:"holder_pid,omitempty"`
	Exited     *Exit  `json:"exited,omitempty"`
}

// Exit is how the shell ended.
type Exit struct {
	// Code is the exit status, or 128 + the signal that killed the shell,
	// as shells report it.
	Code int       `json:"code"`
	At   time.Time `json:"at"`
}

type stopRequest struct {
	GraceMS int64 `json:"grace_ms"`
}

func writeFrame(w io.Writer, t frameType, payload any) error {
	var body []byte
	if payload != nil {
		var err error
		if body, err = json.Marshal(payload); err != nil {
			return err
		}
	}
	frame := make([]byte, 5+len(body))
	frame[0] = byte(t)
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(body)))
	copy(frame[5:], body)
	_, err := w.Write(frame)
	return err
}

func readFrame(r io.Reader) (frameType, []byte, error) {
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(head[1:])
	if n > maxPayload {
		return 0, nil, fmt.Errorf("frame of %d bytes is too large", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return frameType(head[0]), body, nil
}
