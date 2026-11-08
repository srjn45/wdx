package hubapi

import (
	"fmt"
	"strconv"
)

// The control WebSocket's closes, besides the standard ones.
const (
	// CloseVersionUnsupported closes a control connection whose hello names
	// an API version the hub does not serve; the close's reason is
	// BehindNode or BehindHub (hub_version_unsupported).
	CloseVersionUnsupported = 4001
	// BehindNode says the node must be updated, BehindHub the hub.
	BehindNode = "node"
	BehindHub  = "hub"
)

// MinAPIVersion is the oldest version of this API the hub serves on the
// control socket.
const MinAPIVersion = 1

// Behind says which side of a hello's API version, node, of a hub serving
// from MinAPIVersion to current is behind: BehindNode, BehindHub, or empty
// when the hub serves it.
func Behind(node, current string) (string, error) {
	n, err := strconv.Atoi(node)
	if err != nil {
		return "", fmt.Errorf("api_version %q is not a whole number", node)
	}
	c, err := strconv.Atoi(current)
	if err != nil {
		return "", fmt.Errorf("the hub's api_version %q is not a whole number", current)
	}
	switch {
	case n < MinAPIVersion:
		return BehindNode, nil
	case n > c:
		return BehindHub, nil
	}
	return "", nil
}
