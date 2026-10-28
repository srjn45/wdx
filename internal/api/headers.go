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
)
