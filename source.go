// Package openharness embeds the runtime's own source so the builder can
// compile a fresh agent binary anywhere a Go toolchain is available.
package openharness

import "embed"

//go:embed go.mod spec/*.go runtime/*.go cmd/harness/*.go
var Source embed.FS
