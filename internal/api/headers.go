package api

// Headers of requests and responses.
const (
	// DebugIDHeader carries the debug id a request is logged under.
	DebugIDHeader = "Wdx-Debug-Id"
	// LogLevelHeader asks the daemon to log a request at a lower level; only
	// LogLevelDebug is honored.
	LogLevelHeader = "Wdx-Log-Level"
	LogLevelDebug  = "debug"
	// VersionHeader carries the version of the client or the daemon.
	VersionHeader = "Wdx-Version"
	// APIVersionHeader carries, through a session between two nodes, the
	// version of this spec the client or the host was built with.
	APIVersionHeader = "Wdx-Api-Version"
	// StreamUpgrade is the Upgrade a client asks connectHubNode for.
	StreamUpgrade = "wdx-stream"
)

// The versions of this API, as info.version of api/openapi.yaml numbers
// them: CurrentVersion is this build's, raised by every change an older
// client or host cannot handle, and OldestCompatible the oldest this build
// still talks to. Both are kept by hand; a test checks the first against the
// spec.
const (
	CurrentVersion   = 1
	OldestCompatible = 1
)
