package holder

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/srjn45/wdx/internal/screen"
)

// Version is the protocol version this binary speaks. A holder keeps
// running the binary it was started from, so a daemon must speak the
// previous version too.
const Version = 5

// inboxVersion is the first version whose holder takes frameSend. An older
// one would ignore the frame, so the daemon does not send it.
const inboxVersion = 3

// agentInboxVersion is the first version whose holder delivers to an ACA as it
// must, and knows instant messages. An older one would type the message in with
// no look at the screen.
const agentInboxVersion = 4

// remoteVersion is the first version whose holder knows a client relayed
// from another node, which its hello says. An older one would take the relay
// for a client on this machine, so the relay refuses it.
const remoteVersion = 5

// oldestClientVersion is the oldest client a holder serves: the first that
// attaches. Through the attach relay a client may be of another release than
// the holder, older or newer, so the holder serves clients from this version
// on, holding back from an older one what it does not know, as the hello's
// Version says; it says the oldest in OldestClient, and closes the
// connection of a client older still. A newer client decides for itself
// what of an older holder it can use. docs/ATTACH.md.
const oldestClientVersion = 2

// drives reports whether this daemon can drive a holder speaking version v.
// A version up to 2 leaves the daemon's frames as they are, and later ones
// only add to them, so a daemon rolled back from the release that brings one
// keeps the terminals that release started. It accepts versions ahead of its
// own for that, in a release before the one that brings them; docs/ATTACH.md,
// docs/SEND.md.
func drives(v int) bool {
	return v >= 1 && v <= 5
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
	// Takes no version of its own: the daemon asks nothing new of the holder, a
	// holder with no Status in its Hello sends none, and a daemon before it skips
	// the frame. docs/AGENT_STATE.md.
	frameStatus frameType = 12 // holder to daemon: its Status, when it changes
	frameSend   frameType = 13 // daemon to holder: a sendRequest for the inbox
	// Takes no version of its own either: a holder too old for it leaves Confirms
	// out of its hello, and a daemon before it skips the frame. docs/SEND.md.
	frameDelivered frameType = 14 // holder to daemon: a deliveredReport, of messages it typed
	// Take no version of their own either: a holder too old to answer leaves
	// Answers out of its hello, so the daemon sends it no frameAnswer, and a
	// daemon before them sends none. The wdx doc PROMPT_TAB.
	frameAnswer   frameType = 15 // daemon to holder: an answerRequest
	frameAnswered frameType = 16 // holder to daemon: the AnswerReport of the request of its id
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
	Version int    `json:"version"`
	Role    string `json:"role,omitempty"`
	// Remote says the client is on another node, whose name is Node: the
	// daemon relays its bytes, so the socket's peer is the daemon.
	Remote     bool   `json:"remote,omitempty"`
	Node       string `json:"node,omitempty"`
	TerminalID string `json:"terminal_id,omitempty"`
	// OldestClient is the oldest client version the holder serves; 0 from a
	// holder too old to say, which serves version 2 on.
	OldestClient int   `json:"oldest_client,omitempty"`
	PID          int   `json:"pid,omitempty"` // the shell's
	HolderPID    int   `json:"holder_pid,omitempty"`
	Exited       *Exit `json:"exited,omitempty"`
	// HistoryLines is the number the next history line will take.
	HistoryLines int64 `json:"history_lines,omitempty"`
	// Status is nil on a holder that does not read its screen, or is too old to
	// say.
	Status *Status `json:"status,omitempty"`
	// Confirms says it tells which messages it typed, and goes by their ids. One
	// too old to says nothing, and a message handed to it is not known to arrive.
	Confirms bool `json:"confirms,omitempty"`
	// Delivered are the ids of the messages it typed last, including while no
	// daemon was there to be told.
	Delivered []string `json:"delivered,omitempty"`
	// Answers says it answers its program's prompts (frameAnswer). One too old
	// to says nothing.
	Answers bool `json:"answers,omitempty"`
}

// Status is everything a holder tells the daemon about what its program is
// doing, whole each time. docs/AGENT_STATE.md.
type Status struct {
	Activity string `json:"activity"`
	Draft    bool   `json:"draft"`
	// Queued is how many messages wait in the inbox.
	Queued int `json:"queued"`
	// Model is the AI model's name as the program last showed it; "" until it has.
	Model string `json:"model,omitempty"`
	// Prompt is what the program asks while Activity is waiting, when the parser
	// reads it; nil otherwise, and from a holder too old to tell.
	Prompt *screen.Prompt `json:"prompt,omitempty"`
}

// Equal reports whether two statuses say the same, prompts included.
func (s Status) Equal(o Status) bool {
	a, b := s, o
	a.Prompt, b.Prompt = nil, nil
	return a == b && s.Prompt.Equal(o.Prompt)
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
	// ID names the message for a report that it was typed, and makes sending it
	// again harmless. A sender too old to give one gets no report.
	ID string `json:"id,omitempty"`
}

// Answer is the answer to one question of a prompt, given against the prompt
// as the person saw it: its Question and Options, which the holder checks the
// screen still shows before each key. Choose is the index of the option Enter
// is pressed on; Tick the indexes of the boxes a checkbox answer ticks, after
// which Enter goes on its Submit row; Text what a text option takes, typed as
// a paste. The wdx doc PROMPT_TAB.
type Answer struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
	Choose   *int     `json:"choose,omitempty"`
	Tick     []int    `json:"tick,omitempty"`
	Text     string   `json:"text,omitempty"`
}

// answerRequest asks a holder to answer the prompt on its screen: each of
// Answers in turn, one per question of a decision that asks several.
type answerRequest struct {
	ID      string   `json:"id"`
	Answers []Answer `json:"answers"`
}

// AnswerResult is what came of an answer. A daemon takes one it does not know,
// from a newer holder, as a failure.
type AnswerResult string

const (
	// Answered is the prompt closed, or its next question shown, after Enter.
	Answered AnswerResult = "answered"
	// AnswerPromptChanged is a prompt that was not the one sent, or changed
	// with no key of the holder's, as a person's: the holder stopped.
	AnswerPromptChanged AnswerResult = "prompt_changed"
	// AnswerInvalid is an answer the prompt cannot take, as an option of the
	// wrong kind; Reason says why.
	AnswerInvalid AnswerResult = "invalid"
	// AnswerNotConfirmed is a key, Enter included, whose effect did not show in
	// its time; Entered says whether Enter was pressed.
	AnswerNotConfirmed AnswerResult = "not_confirmed"
	// AnswerBusy is another answer under way.
	AnswerBusy AnswerResult = "busy"
	// AnswerEnded is the shell's end.
	AnswerEnded AnswerResult = "ended"
	// AnswerUnread is a holder that reads no screen, and so cannot answer.
	AnswerUnread AnswerResult = "unread"
)

// AnswerReport is what came of an answerRequest.
type AnswerReport struct {
	ID     string       `json:"id"`
	Result AnswerResult `json:"result"`
	// Reason says why an answer was not taken, in words of the holder's own:
	// never a label, question or text of the prompt's.
	Reason string `json:"reason,omitempty"`
	// Question is the index in the request of the answer it stopped at.
	Question int `json:"question"`
	// Entered says Enter was pressed for an answer, so one that failed after
	// may have been taken all the same.
	Entered bool `json:"entered,omitempty"`
}

// deliveredReport says which messages a holder typed.
type deliveredReport struct {
	IDs []string `json:"ids"`
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
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(body))) //nolint:gosec // a frame's payload is far under 4 GiB
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
