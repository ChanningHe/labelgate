// Package version provides build-time version information.
// Variables are overridden via -ldflags at build time.
package version

var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)
