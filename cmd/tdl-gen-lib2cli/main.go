// Command tdl-gen-lib2cli is the lib2cli backend as a TDL plugin.
// tdl runs it for a `target lib2cli` block when it is on PATH.
package main

import (
	"fmt"
	"os"

	"github.com/unstoppablemango/lib2cli/backend"
	"github.com/unstoppablemango/tdl/plugin"
)

// main serves the backend over tdl's plugin protocol.
func main() {
	if err := plugin.Serve(backend.Backend{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
