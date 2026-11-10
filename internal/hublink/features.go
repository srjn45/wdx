package hublink

import (
	"fmt"
	"strconv"

	"github.com/srjn45/wdx/internal/hubapi"
)

// A feature of the link, and the hub API version it needs. The hub welcomes
// a node newer than itself and says its own version: the node decides what
// it can use there, and reports hub_version_unsupported for the rest.
type feature struct {
	name string
	need int
}

var (
	featureNodes   = feature{"the account's nodes", 1}
	featureStreams = feature{"remote access", 1}
	featureDocs    = feature{"the docs push", 1}
)

// hubLacks is a *HubError of hub_version_unsupported when the hub that last
// welcomed the node speaks an API older than f needs, else nil, as it is
// before any welcome: the hub's own answer then says.
func (l *Link) hubLacks(f feature) error {
	l.mu.Lock()
	hubAPI := l.hubAPI
	l.mu.Unlock()
	return lacks(hubAPI, f)
}

func lacks(hubAPI string, f feature) error {
	if hubAPI == "" {
		return nil
	}
	if v, err := strconv.Atoi(hubAPI); err == nil && v >= f.need {
		return nil
	}
	p := hubapi.NewProblem(hubapi.ErrorCodeHubVersionUnsupported,
		fmt.Sprintf("the hub speaks API version %s, and %s needs %d", hubAPI, f.name, f.need))
	p.Details = &map[string]any{"update": hubapi.BehindHub, "api_version": hubAPI, "needs_api_version": f.need}
	return &HubError{Problem: p}
}
