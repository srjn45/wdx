// Package identity holds the tool's name and build information.
//
// During the rebuild the tool ships as "wdx" so it can run side by side with
// the old warden. All values are overridable at build time via -ldflags.
package identity

// Set at build time via -ldflags "-X github.com/srjn45/warden/internal/identity.<Var>=...".
var (
	// Name is the command name.
	Name = "wdx"
	// Version is the release version; "dev" for local builds.
	Version = "dev"
	// Commit is the git commit the binary was built from.
	Commit = "none"
)
