// Package dashboard provides the embedded frontend assets for the Labelgate dashboard.
package dashboard

import (
	"embed"
	"io/fs"
)

//go:embed static
var assets embed.FS

// FS contains the dashboard web UI assets, rooted at static/.
var FS, _ = fs.Sub(assets, "static")
