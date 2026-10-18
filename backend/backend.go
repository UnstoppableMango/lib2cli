// Package backend is the lib2cli TDL backend: it reads functions described
// in a model and plans a shell-callable shim for each.
package backend

import (
	"context"
	"fmt"

	"github.com/unstoppablemango/tdl/ir"
	"github.com/unstoppablemango/tdl/plugin"
)

// Target is the name a target block uses to select this backend.
const Target = "lib2cli"

// Version is the backend's version, reported in the handshake.
const Version = "0.0.0"

// Directive names.
const (
	dirGo     = "go"     // block: go(module, version)
	dirNpm    = "npm"    // block: npm(package, version)
	dirBin    = "bin"    // block: bin(name)
	dirCli    = "cli"    // decl: a type whose Fn fields are commands
	dirFn     = "fn"     // decl: the primitive Fn<Req, Res>
	dirText   = "text"   // decl: a primitive written raw with String(x)
	dirPath   = "path"   // decl: a primitive holding a file path
	dirStream = "stream" // decl: a primitive read from stdin or written to stdout
	dirFunc   = "func"   // field: func(name), the function's real name
	dirWhole  = "whole"  // field: pass the request as one argument
)

// Backend is the lib2cli backend.
type Backend struct{}

var str = []ir.LiteralKind{ir.LiteralKind_LITERAL_KIND_STRING}

// Describe names the backend and declares its directives, which tdl checks
// a target block against.
func (Backend) Describe() plugin.Description {
	block := plugin.On(plugin.OnBlock)
	decl := plugin.On(plugin.OnDecl)
	field := plugin.On(plugin.OnField)

	return plugin.Description{
		Name:    Target,
		Version: Version,
		Directives: []*plugin.DirectiveSpec{
			{Name: dirGo, MinArgs: 2, MaxArgs: 2, ArgKinds: str, Placements: block},
			{Name: dirNpm, MinArgs: 2, MaxArgs: 2, ArgKinds: str, Placements: block},
			{Name: dirBin, MinArgs: 1, MaxArgs: 1, ArgKinds: str, Placements: block},
			{Name: dirCli, Placements: decl},
			{Name: dirFn, Placements: decl},
			{Name: dirText, Placements: decl},
			{Name: dirPath, Placements: decl},
			{Name: dirStream, Placements: decl},
			{Name: dirFunc, MinArgs: 1, MaxArgs: 1, ArgKinds: str, Placements: field},
			{Name: dirWhole, Placements: field},
		},
	}
}

// Generate plans the model and reports the plan's diagnostics. It writes
// no files yet.
func (Backend) Generate(_ context.Context, req *plugin.Request) (*plugin.Response, error) {
	plan, diags := Build(req)
	if !hasErrors(diags) {
		// Code generation comes next; until then a model that plans
		// cleanly says so rather than writing nothing silently.
		diags = append(diags, &plugin.Diagnostic{
			Severity: plugin.Severity_SEVERITY_WARNING,
			Message:  fmt.Sprintf("planned %d command(s) for %s; code generation is not implemented yet", len(plan.Commands), plan.Package),
		})
	}
	return &plugin.Response{Diagnostics: diags}, nil
}

// hasErrors says whether any diagnostic is an error.
func hasErrors(diags []*plugin.Diagnostic) bool {
	for _, d := range diags {
		if d.GetSeverity() == plugin.Severity_SEVERITY_ERROR {
			return true
		}
	}
	return false
}
