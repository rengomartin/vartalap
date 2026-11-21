// Package templates embeds the HTML templates into the binary.
package templates

import "embed"

//go:embed layouts pages components
var FS embed.FS
