# lib2cli

Generate a small shim, in a library's own language, that runs one of its functions from the shell.

lib2cli is a [TDL](https://github.com/UnstoppableMango/tdl) backend plugin, `tdl-gen-lib2cli`.
A `.tdl` file describes a library's functions and binds them to a Go module or npm package, and `tdl gen` writes a buildable Go or TypeScript project whose binary calls them.

Early design, nothing built yet. See [docs/design.md](docs/design.md).
