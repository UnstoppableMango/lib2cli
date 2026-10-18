// Package golang holds the sources lib2cli copies into every generated Go
// project: the runtime in shim/ and the build-time generator in generate/.
package golang

import _ "embed"

// Shim is the runtime, shim/shim.go.
//
//go:embed shim/shim.go
var Shim string

// Generate is the generator, generate/main.go, which go generate runs to
// write commands.go from the library's real signatures.
//
//go:embed generate/main.go
var Generate string
