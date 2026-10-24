// Package assets holds the Pantech logo the server shows AI apps (its
// serverInfo icons) and serves at /icon.svg, /icon.png and /favicon.ico.
package assets

import _ "embed"

// IconSVG is the Pantech Orbit mark; it follows the viewer's light or dark theme.
//
//go:embed icon.svg
var IconSVG []byte

// IconPNG is the same mark as a 32×32 PNG.
//
//go:embed icon.png
var IconPNG []byte
