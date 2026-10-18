# lib2cli

Generate a small shim, in a library's own language, that runs one of its functions from the shell.

lib2cli is a [TDL](https://github.com/UnstoppableMango/tdl) backend plugin, `tdl-gen-lib2cli`.
A `.tdl` file describes a library's functions and binds them to a Go module or npm package, and `tdl gen` writes a buildable Go or TypeScript project whose binary calls them.

## Status

The plugin reads a model and plans the commands it would generate, reporting anything it cannot map; it writes no code yet.
See [docs/design.md](docs/design.md) for where it is going.

```tdl
package mdx

import "prelude/lib2cli.tdl" as lib

type CompileRequest {
  file: lib.Text
}

type Mdx {
  compile: lib.Fn<CompileRequest, lib.Text>
}

target lib2cli for mdx {
  npm("@mdx-js/mdx", "3.1.0")
  Mdx => cli
}
```

`Fn`, `Text`, `Path` and `Stream` come from [prelude/lib2cli.tdl](prelude/lib2cli.tdl), imported by a path relative to the importing file (here, a file at the repo root); the examples import it as `lib`.
Each field of the type tagged `cli` is a command; `Type.field => func("name")` names the real function, `Type.field => whole` passes the request as one argument, and `bin("name")` names the binary.

## Development

```bash
nix develop        # go, gomod2nix, tdl
go test ./...      # golden tests; needs tdl on PATH
go test ./... -update
gomod2nix generate --outdir nix   # after go.mod changes
nix flake check
```
