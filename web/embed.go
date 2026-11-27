// Package web holds the builder UI (plain HTML/CSS/JS, no build step).
package web

import "embed"

//go:embed index.html app.js styles.css og.png
var FS embed.FS
