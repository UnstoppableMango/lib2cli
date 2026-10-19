# lib2cli

Generate a small shim, in a library's own language, that runs one of its functions from the shell.

lib2cli is a [TDL](https://github.com/UnstoppableMango/tdl) backend plugin, `tdl-gen-lib2cli`.
A `.tdl` file describes a library's functions and binds them to a Go module or npm package, and `tdl gen` writes a buildable Go or TypeScript project whose binary calls them.

## Status

For a Go module, `tdl gen` writes a project that builds with `go generate && go build`: `go.mod` pinning the library, `lib2cli.json` holding the commands, `main.go`, `shim.go`, the runtime that parses flags, and `generate.go`.
`go generate` runs `generate.go`, which reads the library's real signatures from the compiler's export data and writes `commands.go`, a purpose-built call per command, so the shim uses no reflection and a signature the model does not fit fails the build.
TypeScript is planned but not generated yet.
See [docs/design.md](docs/design.md) for where it is going.

```tdl
package mdx

import "lib/lib2cli.tdl" as lib

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

`Fn`, `Text`, `Path` and `Stream` come from the lib2cli module, [lib/lib2cli.tdl](lib/lib2cli.tdl), imported by a path relative to the importing file (here, a file at the repo root); the examples import it as `lib`.
Each field of the type tagged `cli` is a command; `Type.field => func("name")` names the real function, `Type.field => whole` passes the request as one argument, and `bin("name")` names the binary.
Subcommands and flags are the field names in kebab case, so two fields that kebab to the same name are an error, as is a request field named `json` or `help`, which every command reserves.

A Go shim reads the rest from each function's signature when it is built: a leading `context.Context` is injected and cancelled on SIGINT, a trailing `error` sets the exit code, and the arguments decode with `encoding/json` into the parameters' real Go types, so named types, pointers for options and integer ranges all follow the library.
A `whole` request's fields are matched by json tag, or by name ignoring case and underscores.
In Go, a field calls the function named after it with the first letter upper-cased, so `convertFile` calls `ConvertFile`.
On the command line a list flag repeats, a map flag repeats as `key=value`, an object flag takes JSON, a stream flag takes a file or `-` for stdin, and `--json` reads every argument as one object from stdin instead.
A string, path, bytes or `Text` result is written raw, a stream is copied, and anything else is written as JSON; a failure exits 1 with the message on stderr, and a bad command line exits 2.
The generated project writes no `go.sum`; whoever builds it resolves the library.

## Development

```bash
nix develop        # go, gomod2nix, tdl
go test ./...      # golden tests; needs tdl on PATH
go test ./... -update
tdl gen backend/plan.tdl   # after editing the plan types
gomod2nix generate --outdir nix   # after go.mod changes
nix flake check
```
