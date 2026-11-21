// Package static embeds vendored CSS, JavaScript, fonts and the brand assets
// into the binary.
package static

import "embed"

//go:embed css js fonts *.svg
var FS embed.FS
