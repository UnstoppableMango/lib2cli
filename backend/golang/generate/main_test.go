package main

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"strings"
	"testing"
)

// std imports the standard library, shared so every package sees the same
// types.
var std = importer.Default()

// lib type-checks src as the package example.com/lib.
func lib(t *testing.T, src string) (*types.Package, *types.Package) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "lib.go", "package lib\n\n"+src, 0)
	if err != nil {
		t.Fatal(err)
	}
	imp := std
	pkg, err := (&types.Config{Importer: imp}).Check("example.com/lib", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	io, err := imp.Import("io")
	if err != nil {
		t.Fatal(err)
	}
	return pkg, io
}

var (
	str = typ{Kind: "string"}
	num = typ{Kind: "int"}
)

func TestMismatch(t *testing.T) {
	pkg, io := lib(t, `
import (
	"context"
	"fmt"
)

func Variadic(xs ...string) {}
func Two(a, b string) {}
func Pair() (int, int) { return 0, 0 }
func Ctx(ctx context.Context, s string) {}
func Float(f float64) {}
func Text() struct{} { return struct{}{} }
func Whole(s string) {}
type Req struct{ name string }
func Field(r Req) {}
func Map(m map[int]string) {}
func Stringer() fmt.Stringer { return nil }
`)
	for _, tt := range []struct {
		name string
		c    command
		want string
	}{
		{"missing", command{Func: "Nope"}, "example.com/lib has no function Nope"},
		{"variadic", command{Func: "Variadic", Params: []param{{Name: "xs", Type: str}}}, "Variadic is variadic"},
		{"arity", command{Func: "Two", Params: []param{{Name: "a", Type: str}}}, "Two takes 2 argument(s), but the model gives it 1"},
		{"context is not counted", command{Func: "Ctx"}, "Ctx takes 1 argument(s), but the model gives it 0"},
		{"results", command{Func: "Pair", Result: num}, "Pair returns 2 values"},
		{"param type", command{Func: "Float", Params: []param{{Name: "f", Type: num}}}, "f: the model gives a int, but the function takes float64"},
		{"result type", command{Func: "Text", Result: typ{Kind: "text"}}, "result: the model writes a text, but the function returns struct{}"},
		{"whole needs a struct", command{Func: "Whole", Whole: true, Params: []param{{Name: "s", Type: str}}}, "Whole takes string, but a whole command passes a struct"},
		{"whole field", command{Func: "Field", Whole: true, Params: []param{{Name: "name", Type: str}}}, "example.com/lib.Req has no exported field for name"},
		{"map key", command{Func: "Map", Params: []param{{Name: "m", Type: typ{Kind: "map", Elem: &str}}}}, "m: the model gives a map of string, but the function takes map[int]string"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := generate(&plan{Commands: []command{tt.c}}, pkg, io, nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want an error containing %q", err, tt.want)
			}
		})
	}

	// fmt.Stringer is a text result, but not a string one.
	if _, err := generate(&plan{Commands: []command{{Func: "Stringer", Result: typ{Kind: "text"}}}}, pkg, io, nil); err != nil {
		t.Error(err)
	}
}

func TestGenerate(t *testing.T) {
	pkg, io := lib(t, `
import (
	"context"
	"io"
	"net/url"
	"time"
)

type Level uint8
type Name string
func (n *Name) String() string { return string(*n) }

func Any(v any, n any) {}
func Levels(ctx context.Context, ls []Level, m map[Name]*Level) error { return nil }
func URL(u *url.URL, d time.Duration) *Name { return nil }
func Read(s Name) []byte { return nil }
type Bytes []byte
func Maybe(in io.Reader, b *Bytes) {}
type Req struct {
	Name string
	RawData Bytes
	In string
}
func Whole(r *Req) {}
`)
	src, err := generate(&plan{Commands: []command{
		{Name: "any", Func: "Any", Params: []param{{Name: "v", Type: str}, {Name: "n", Type: num}}},
		{Name: "levels", Func: "Levels", Params: []param{
			{Name: "ls", Type: typ{Kind: "list", Elem: &num}},
			{Name: "m", Type: typ{Kind: "map", Elem: &typ{Kind: "option", Elem: &num}}},
		}},
		{Name: "url", Func: "URL", Params: []param{{Name: "u", Type: typ{Kind: "object"}}, {Name: "d", Type: num}}, Result: typ{Kind: "option", Elem: &typ{Kind: "text"}}},
		{Name: "read", Func: "Read", Params: []param{{Name: "s", Type: typ{Kind: "stream"}}}, Result: typ{Kind: "bytes"}},
		{Name: "maybe", Func: "Maybe", Params: []param{
			{Name: "in", Type: typ{Kind: "option", Elem: &typ{Kind: "stream"}}},
			{Name: "b", Type: typ{Kind: "option", Elem: &typ{Kind: "bytes"}}},
		}},
		{Name: "whole", Func: "Whole", Whole: true, Params: []param{
			{Name: "name", Type: str},
			{Name: "raw_data", Type: typ{Kind: "bytes"}},
			{Name: "in", Type: typ{Kind: "stream"}},
		}},
	}}, pkg, io, map[string]bool{"url": true})
	if err != nil {
		t.Fatal(err)
	}
	compiles(t, pkg, src)
	for _, want := range []string{
		// A name the shim declares is not reused for an import.
		`urlpkg2 "net/url"`,
		`"time"`,
		"P0 any `json:\"v\"`",
		"P1 map[lib.Name]*lib.Level `json:\"m\"`",
		"return lib.Levels(ctx, req.P0, req.P1)",
		"P0 *urlpkg2.URL  `json:\"u\"`",
		"P1 time.Duration `json:\"d\"`",
		// Only *Name has String, so the pointer is written as it is.
		"if res == nil {\n\t\t\t\treturn nil\n\t\t\t}\n\t\t\treturn writeString(out, res.String())",
		"v1, err := in.open(req.P0)\n\t\t\tif err != nil {\n\t\t\t\treturn argError{0, err}\n\t\t\t}\n\t\t\tv2, err := readAll[lib.Name](v1)",
		// An optional stream or bytes is left zero when it is absent.
		"P0 *string `json:\"in\"`",
		"var v1 io.Reader\n\t\t\tif req.P0 != nil {\n\t\t\t\tv2, err := in.open(*req.P0)",
		"v4 := lib.Bytes(*req.P1)\n\t\t\t\tv3 = &v4",
		"lib.Maybe(v1, v3)",
		// A whole request matches fields by name, ignoring underscores.
		"v3 := new(lib.Req)\n\t\t\tv3.Name = req.P0\n\t\t\tv3.RawData = lib.Bytes(req.P1)\n\t\t\tv3.In = v2\n\t\t\tlib.Whole(v3)",
		"return writeBytes(out, res)",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("commands.go does not contain %q:\n%s", want, src)
		}
	}
}

// TestReserved never imports a package under a name commands.go itself
// declares.
func TestReserved(t *testing.T) {
	fset := token.NewFileSet()
	dep, err := (&types.Config{}).Check("example.com/commands", fset, []*ast.File{parse(t, fset, "package commands\n\ntype T int\n")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	imp := importerFunc(func(path string) (*types.Package, error) {
		if path == dep.Path() {
			return dep, nil
		}
		return std.Import(path)
	})
	pkg, err := (&types.Config{Importer: imp}).Check("example.com/lib", fset, []*ast.File{
		parse(t, fset, "package lib\n\nimport \"example.com/commands\"\n\nfunc F(t commands.T) {}\n"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	io, err := std.Import("io")
	if err != nil {
		t.Fatal(err)
	}
	src, err := generate(&plan{Commands: []command{{Name: "f", Func: "F", Params: []param{{Name: "t", Type: num}}}}}, pkg, io, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `commandspkg2 "example.com/commands"`) {
		t.Errorf("commands.go imports a package as commands:\n%s", src)
	}
}

type importerFunc func(string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }

func parse(t *testing.T, fset *token.FileSet, src string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// compiles type-checks commands.go with the runtime, as package main.
func compiles(t *testing.T, lib *types.Package, src []byte) {
	t.Helper()
	shim, err := os.ReadFile("../shim/shim.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	files := []*ast.File{
		parse(t, fset, strings.Replace(string(shim), "\npackage shim\n", "\npackage main\n", 1)),
		parse(t, fset, string(src)),
	}
	imp := importerFunc(func(path string) (*types.Package, error) {
		if path == lib.Path() {
			return lib, nil
		}
		return std.Import(path)
	})
	if _, err := (&types.Config{Importer: imp}).Check("main", fset, files, nil); err != nil {
		t.Errorf("commands.go does not compile: %v\n%s", err, src)
	}
}
