package hubapi

import "strconv"

// The control WebSocket's closes, besides the standard ones.
const (
	// CloseVersionUnsupported closes a control connection whose hello names
	// an API version older than the hub serves; the close's reason is
	// BehindNode (hub_version_unsupported). A hub from before every newer
	// node was welcomed closes a newer node with BehindHub.
	CloseVersionUnsupported = 4001
	// BehindNode says the node must be updated, BehindHub the hub.
	BehindNode = "node"
	BehindHub  = "hub"
)

// MinAPIVersion is the oldest version of this API the hub serves, on the
// control socket and on every request a node sends.
const MinAPIVersion = 1

// Served reports whether a hub serves a node of API version node: any from
// MinAPIVersion up, newer than the hub's own included, since the newer side
// decides what it can use. A version that is no whole number is not.
func Served(node string) bool {
	n, err := strconv.Atoi(node)
	return err == nil && n >= MinAPIVersion
}
