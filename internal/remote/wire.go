package remote

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"filippo.io/cpace"
)

// The versions of what the two nodes speak inside a stream: protocolVersion
// is this build's, which a client sends, and oldestProtocolVersion the oldest
// a host still accepts. A host refuses one outside its range with
// node_version_unsupported, giving both in the refusal, so that a later
// client can tell whether to speak the host's version or have it updated.
const (
	protocolVersion       = 1
	oldestProtocolVersion = 1
)

// maxMessage bounds a message's size, so that a peer cannot make the other
// read without end.
const maxMessage = 64 << 10

// The messages' types. The client's first says what the stream is for:
// open, stream or close.
const (
	msgOpen     = "open"     // client: a session, with CPace's first message
	msgExchange = "exchange" // host: CPace's answer
	msgConfirm  = "confirm"  // client: its MAC
	msgOpened   = "opened"   // host: its MAC, the session and its timeout
	msgStream   = "stream"   // client: a stream of a session open
	msgClose    = "close"    // client: close a session
	msgOK       = "ok"       // host: stream or close done
	msgRefused  = "refused"  // host: a refusal, with its code
)

// message is every message, each type using some of the fields. It is never
// logged: CPace's messages and the MACs are in it.
type message struct {
	Type string `json:"type"`
	// Version is the protocol's: the client's in its first message, the
	// host's in a refusal, which gives Oldest, the oldest it accepts, too.
	Version int `json:"version,omitempty"`
	Oldest  int `json:"oldest,omitempty"`
	// Step is the 30-second step the client computed the code for.
	Step  int64  `json:"step,omitempty"`
	CPace []byte `json:"cpace,omitempty"`
	MAC   []byte `json:"mac,omitempty"`
	// Session is the session's id; IdleTimeoutMS the host's timeout.
	Session       string `json:"session,omitempty"`
	IdleTimeoutMS int64  `json:"idle_timeout_ms,omitempty"`
	// Code, Detail and Reason are a refusal's.
	Code   Code   `json:"code,omitempty"`
	Detail string `json:"detail,omitempty"`
	Reason Reason `json:"reason,omitempty"`
}

// write sends m as its length in 4 bytes, then its JSON.
func write(w io.Writer, m message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	frame := binary.BigEndian.AppendUint32(make([]byte, 0, 4+len(b)), uint32(len(b))) //nolint:gosec // json of a message is far under 4 GiB
	_, err = w.Write(append(frame, b...))
	return err
}

// read reads one message, of type want or a refusal, which it returns as
// an *Error.
func read(r io.Reader, want ...string) (message, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return message{}, err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n > maxMessage {
		return message{}, fmt.Errorf("a message of %d bytes, over %d", n, maxMessage)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return message{}, err
	}
	var m message
	if err := json.Unmarshal(b, &m); err != nil {
		return message{}, fmt.Errorf("a message unreadable: %w", err)
	}
	if m.Type == msgRefused {
		return message{}, m.refusal()
	}
	for _, w := range want {
		if m.Type == w {
			return m, nil
		}
	}
	return message{}, fmt.Errorf("a message of type %q, not %v", m.Type, want)
}

// known is the codes a host refuses with.
var known = map[Code]bool{
	CodeRemoteAccessOff: true, CodeCodeNotSetUp: true, CodeCodeWrong: true, CodeNodeKeyMismatch: true,
	CodeNodeVersionUnsupported: true, CodeSessionClosed: true,
}

// refusal is the *Error of a refused message.
func (m message) refusal() *Error {
	code, detail := m.Code, m.Detail
	if !known[code] {
		code, detail = CodeNodeVersionUnsupported, fmt.Sprintf("the node refused with %q, which this wdx does not know: update it", m.Code)
	}
	e := refusal(code, "the node refused: %s", detail)
	if m.Reason != "" {
		e.Details = map[string]any{"reason": string(m.Reason)}
	}
	if code == CodeNodeVersionUnsupported && m.Version > 0 {
		update := "host"
		if m.Oldest > protocolVersion {
			update = "client"
		}
		e.Details = map[string]any{"update": update, "host_version": m.Version, "host_oldest": m.Oldest}
	}
	return e
}

// refuse sends e as a refusal, with this host's protocol range.
func refuse(w io.Writer, e *Error, reason Reason) error {
	return write(w, message{Type: msgRefused, Code: e.Code, Detail: e.Err.Error(), Reason: reason, Version: protocolVersion, Oldest: oldestProtocolVersion})
}

// binding is what both sides agree a session is between: the TLS channel's
// exporter value and both nodes' keys.
type binding struct {
	exporter     []byte
	client, host ed25519.PublicKey
	step         int64
	version      int // the protocol's, the client's
}

// context is CPace's context: the two keys as its identities, and the
// exporter, the protocol and the step as its additional data, so that keys
// derived in another channel, between other keys or for another step differ.
func (b binding) context() *cpace.ContextInfo {
	ad := append([]byte("wdx remote v"+strconv.Itoa(b.version)+" step "+strconv.FormatInt(b.step, 10)+" "), b.exporter...)
	return cpace.NewContextInfo(string(b.client), string(b.host), ad)
}

// mac is role's MAC under CPace's key of the exporter and both keys: the
// client's and the host's differ, so that neither is the other's echo.
func (b binding) mac(key []byte, role string) []byte {
	h := hmac.New(sha256.New, key)
	for _, part := range [][]byte{[]byte("wdx remote confirm " + role), b.exporter, b.client, b.host} {
		h.Write(binary.BigEndian.AppendUint16(nil, uint16(len(part)))) //nolint:gosec // each part is short
		h.Write(part)
	}
	return h.Sum(nil)
}
