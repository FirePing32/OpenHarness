// Package web holds the builder UI (plain HTML/CSS/JS, no build step).
package web

import "embed"

//go:embed index.html app.js styles.css og.png favicon.ico apple-touch-icon.png icon-512.png robots.txt
var FS embed.FS
