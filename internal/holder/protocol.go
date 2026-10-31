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
const Version = 4

// inboxVersion is the first version whose holder takes frameSend. An older
// one would ignore the frame, so the daemon does not send it.
const inboxVersion = 3

// agentInboxVersion is the first version whose holder delivers to an ACA as it
// must, and knows instant messages. An older one would type the message in with
// no look at the screen.
const agentInboxVersion = 4

// drives reports whether this daemon can drive a holder speaking version v.
// A version up to 2 leaves the daemon's frames as they are, and later ones
// only add to them, so a daemon rolled back from the release that brings one
// keeps the terminals that release started. It accepts versions ahead of its
// own for that, in a release before the one that brings them; docs/ATTACH.md,
// docs/SEND.md.
func drives(v int) bool {
	return v >= 1 && v <= 4
}

// A frame is one byte of type, the payload's length as 4 bytes big-endian,
// and the payload: JSON for control frames.
type frameType byte

const (
	frameHello   frameType = 1 // both ways, first: the connecting side's Hello, then the holder's
	frameExited  frameType = 2 // holder to daemon and clients: the shell ended, with an Exit
	frameStop    frameType = 3 // daemon to holder: stop the shell, answered by frameExited
	frameRelease frameType = 4 // daemon to holder: the exit is recorded, quit
	// Version 2: clients attach (docs/ATTACH.md).
	frameAttach   frameType = 5  // client to holder: an attachRequest
	frameHistory  frameType = 6  // holder to client: HistoryLines, the lines before the snapshot
	frameSnapshot frameType = 7  // holder to client: a Snapshot; the output frames after it paint the screen first
	frameOutput   frameType = 8  // holder to client: raw output
	frameInput    frameType = 9  // client to holder: raw input for the shell
	frameResize   frameType = 10 // client to holder: a Size
	frameResized  frameType = 11 // holder to client: the terminal's new Size, in order with the output
	// 12 is taken by frameActivity (docs/AGENT_STATE.md), 14 by frameInbox
	// (docs/SEND.md).
	frameSend frameType = 13 // daemon to holder: a sendRequest for the inbox
)

// Roles a connecting side gives in its hello; a hello without one is a
// version 1 daemon's.
const (
	roleDaemon = "daemon"
	roleClient = "client"
)

// maxPayload bounds a frame read, so a broken peer cannot make the reader
// allocate without end.
const maxPayload = 1 << 20

// Hello is what each side says first: the connecting side its version and
// role, the holder the rest.
type Hello struct {
	Version    int    `json:"version"`
	Role       string `json:"role,omitempty"`
	TerminalID string `json:"terminal_id,omitempty"`
	PID        int    `json:"pid,omitempty"` // the shell's
	HolderPID  int    `json:"holder_pid,omitempty"`
	Exited     *Exit  `json:"exited,omitempty"`
	// HistoryLines is the number the next history line will take.
	HistoryLines int64 `json:"history_lines,omitempty"`
}

// Size is a terminal's size in cells.
type Size struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// attachRequest asks for the screen, after History lines of history: all
// of it when negative.
type attachRequest struct {
	Size
	History int `json:"history"`
}

// HistoryLines are lines of history, numbered from From.
type HistoryLines struct {
	From  int64    `json:"from"`
	Lines []string `json:"lines"`
}

// Snapshot heads the screen as an attaching client gets it: the output
// frames after it paint the screen, then carry the output from Pos on.
type Snapshot struct {
	Size
	// Pos is the position in the shell's output the screen shows.
	Pos int64 `json:"pos"`
	// HistoryLines is the number of the first history line not sent: the
	// screen continues the history from there.
	HistoryLines int64 `json:"history_lines"`
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

type sendRequest struct {
	Prompt  string `json:"prompt"`
	Instant bool   `json:"instant,omitempty"`
}

func writeFrame(w io.Writer, t frameType, payload any) error {
	var body []byte
	if payload != nil {
		var err error
		if body, err = json.Marshal(payload); err != nil {
			return err
		}
	}
	return writeRaw(w, t, body)
}

// writeRaw writes a frame whose payload is body as is.
func writeRaw(w io.Writer, t frameType, body []byte) error {
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
