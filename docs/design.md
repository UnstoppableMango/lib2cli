# lib2cli design

lib2cli generates a small shim, written in a library's own language, that runs one of the library's functions from the shell.
Decisions below were settled in the project thread on 2026-10-16 (Q1 to Q27).

## Goal and consumers

- The first consumer is a2b and Nix pipelines: a library function with no CLI gets a shim, and a2b wraps the shim as a builder. lib2cli itself knows nothing about Nix. (Q1)
- In scope is any function whose inputs and outputs can be serialized. Paths and streams get first-class handling. (Q3)

## The spec is TDL

- A function is described in [TDL](https://github.com/UnstoppableMango/tdl). Introspecting a library's own types comes later, and only as a way to write the TDL. (Q2, Q10, Q17)
- lib2cli is a TDL backend plugin written in Go, in its own repo: `tdl gen` on a file with a `target lib2cli` block runs `tdl-gen-lib2cli`. (Q8, Q17, Q21)
- Functions use the pattern TDL's protobuf backend already uses: `primitive Fn: type -> type -> type`, a type whose fields are `Fn<Req, Res>`, and target-block tags. (Q17)
- `Fn`, `path`, `stream` and `Text` are primitives in a lib2cli prelude import, not in TDL's standard prelude. `Text` means converting with `String(x)` and writing the result raw. (Q20, Q27)
- A binding directive names the library and picks the language: `go("github.com/x/y", "v1.2.3")` or `npm("@x/y", "1.2.3")`. Each `.tdl` file describes one library. Further directives map model names to real function names. (Q18, Q22)
- The request type's fields become positional parameters in field order. A `whole` directive passes the struct as a single argument. (Q19)
- Types outside the prelude's set are rejected at generation time. (Q10)
- TDL is pinned as both a flake input and a Go module, and lib2cli takes TDL's churn as it comes. (Q23)

```tdl
package mdx

import "lib2cli/prelude" as _

type CompileRequest {
  file: string
  options: CompileOptions?
}

type Mdx {
  compile: Fn<CompileRequest, Text>
}

target lib2cli for mdx {
  npm("@mdx-js/mdx", "3.1.0")
}
```

The example is illustrative, so its directive spellings may change during implementation.

## Shell convention

The convention is the same in every target language. (Q4)

- Each parameter is a `--flag`. In `--json` mode, the whole argument object is read from stdin.
- The result goes to stdout, raw for string, bytes and `Text`, and as JSON for anything else. VFile messages go to stderr as warnings. (Q27)
- An error or exception gives a nonzero exit code, with the message on stderr.
- When a file lists several functions, each one is a subcommand. The binary is named after the library unless a directive overrides it. (Q13)
- Language idioms are handled without anything in the spec. In Go, an `error` return sets the exit code, `(T, error)` is unwrapped, and a `context.Context` is injected and cancelled on SIGINT. In TS, Promises are awaited and a thrown error sets the exit code. (Q12)

## Output

- The output is a buildable project, `go.mod` or `package.json`, pinned to the library version. Building it is a2b's job. (Q6)
- lib2cli never touches the network and writes no lockfile. The builder resolves dependencies and their hashes. (Q14)

## Scope of v1

- Targets are Go and TypeScript. (Q5)
- Only free functions are supported. Calls that need a receiver or a processor chain, such as `retext().use(p).process(x)`, come next, and retext is the acceptance test for that work. (Q11, Q26)
- v1 is done when the fixture libraries pass the e2e check in both languages and `@mdx-js/mdx` `compile` works end to end. (Q24, Q26)
- Tests are golden files for the generated source, plus a `nix flake check` that generates, builds and runs a shim against a fixture library in each language. (Q16)

## Repo and follow-ups

- The repo is a new public `lib2cli`, added under `utilities.ts` in vcs, MIT licensed and set up as a Nix flake. (Q7)
- The vcs PR comes first, then this document moves into the new repo, then the plugin is built. (Q25)
- An a2b builder, `lib2cli.build { spec = ...; }` returning a derivation with the shim in `bin/`, follows once v1 works. (Q15)
