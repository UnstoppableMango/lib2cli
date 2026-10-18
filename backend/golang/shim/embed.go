package shim

import _ "embed"

// Source is this package's runtime, shim.go, which the generator copies
// into every generated project.
//
//go:embed shim.go
var Source string
