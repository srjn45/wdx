package node

import "sync"

// Reason is why the hub closes a node's control connection.
type Reason string

// The reasons, as the node is told them in its bye.
const (
	// ReasonRevoked is a node removed, or whose user was removed: for good.
	ReasonRevoked Reason = "revoked"
	// ReasonDisabled is a node whose user was disabled: until enabled.
	ReasonDisabled Reason = "disabled"
	// ReasonShuttingDown is the hub stopping: the node connects again.
	ReasonShuttingDown Reason = "shutting_down"
)

// Conn is a node's open control connection, as the registry knows it.
type Conn struct {
	NodeID, UserID string
	// bye tells the connection to say goodbye with the reason and close;
	// send hands it a message to send the node, reporting whether it took
	// it. Neither may block.
	bye  func(Reason)
	send func(any) bool
	seq  uint64 // the order connections were added in
}

// Registry is the open control connections, in memory, by node: what makes
// a node online, and what cuts it off.
type Registry struct {
	mu    sync.Mutex
	conns map[string]map[*Conn]struct{} // by node id
	seq   uint64
}

func newRegistry() *Registry { return &Registry{conns: map[string]map[*Conn]struct{}{}} }

// Add registers a connection of node nodeID of userID, which bye closes with
// a reason and send hands a message to send the node, neither blocking; the
// connection removes itself when it ends.
func (r *Registry) Add(nodeID, userID string, bye func(Reason), send func(any) bool) *Conn {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	c := &Conn{NodeID: nodeID, UserID: userID, bye: bye, send: send, seq: r.seq}
	if r.conns[nodeID] == nil {
		r.conns[nodeID] = map[*Conn]struct{}{}
	}
	r.conns[nodeID][c] = struct{}{}
	return c
}

// Remove forgets the connection c, which has ended.
func (r *Registry) Remove(c *Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.conns[c.NodeID], c)
	if len(r.conns[c.NodeID]) == 0 {
		delete(r.conns, c.NodeID)
	}
}

// Online reports whether the node nodeID has a control connection now.
func (r *Registry) Online(nodeID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.conns[nodeID]) > 0
}

// Send hands v to the newest control connection of the node nodeID, to send
// the node, reporting whether one took it: none does when the node has no
// connection, or its connection has too much to send already.
func (r *Registry) Send(nodeID string, v any) bool {
	r.mu.Lock()
	var newest *Conn
	for c := range r.conns[nodeID] {
		if newest == nil || c.seq > newest.seq {
			newest = c
		}
	}
	r.mu.Unlock()
	return newest != nil && newest.send != nil && newest.send(v)
}

// SendUser hands v to the newest control connection of each node of userID
// but except, as Send does for one, and returns the nodes it was handed to.
func (r *Registry) SendUser(userID, except string, v any) []string {
	r.mu.Lock()
	newest := map[string]*Conn{}
	for nodeID, set := range r.conns {
		if nodeID == except {
			continue
		}
		for c := range set {
			if c.UserID == userID && (newest[nodeID] == nil || c.seq > newest[nodeID].seq) {
				newest[nodeID] = c
			}
		}
	}
	r.mu.Unlock()
	var sent []string
	for nodeID, c := range newest {
		if c.send != nil && c.send(v) {
			sent = append(sent, nodeID)
		}
	}
	return sent
}

// CloseAll closes every connection with reason, as the hub does when it
// stops.
func (r *Registry) CloseAll(reason Reason) {
	r.cut(func(*Conn) bool { return true }, reason)
}

// cut closes the connections match picks with reason and returns them, one
// per node.
func (r *Registry) cut(match func(*Conn) bool, reason Reason) []*Conn {
	r.mu.Lock()
	var cut []*Conn
	var byes []func(Reason)
	for _, set := range r.conns {
		var first *Conn
		for c := range set {
			if match(c) {
				byes = append(byes, c.bye)
				if first == nil {
					first = c
				}
			}
		}
		if first != nil {
			cut = append(cut, first)
		}
	}
	r.mu.Unlock()
	for _, bye := range byes {
		bye(reason)
	}
	return cut
}
