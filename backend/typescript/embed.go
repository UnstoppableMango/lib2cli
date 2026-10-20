// Package typescript holds the source lib2cli copies into every generated
// TypeScript project: the runtime in shim/.
package typescript

import _ "embed"

// Shim is the runtime, shim/shim.ts.
//
//go:embed shim/shim.ts
var Shim string
