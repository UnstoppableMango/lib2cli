package main

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

// lib type-checks src as the package example.com/lib.
func lib(t *testing.T, src string) (*types.Package, *types.Package) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "lib.go", "package lib\n\n"+src, 0)
	if err != nil {
		t.Fatal(err)
	}
	imp := importer.Default()
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
`)
	src, err := generate(&plan{Commands: []command{
		{Name: "any", Func: "Any", Params: []param{{Name: "v", Type: str}, {Name: "n", Type: num}}},
		{Name: "levels", Func: "Levels", Params: []param{
			{Name: "ls", Type: typ{Kind: "list", Elem: &num}},
			{Name: "m", Type: typ{Kind: "map", Elem: &typ{Kind: "option", Elem: &num}}},
		}},
		{Name: "url", Func: "URL", Params: []param{{Name: "u", Type: typ{Kind: "object"}}, {Name: "d", Type: num}}, Result: typ{Kind: "option", Elem: &typ{Kind: "text"}}},
		{Name: "read", Func: "Read", Params: []param{{Name: "s", Type: typ{Kind: "stream"}}}, Result: typ{Kind: "bytes"}},
	}}, pkg, io, map[string]bool{"url": true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		// A name the shim declares is not reused for an import.
		`urlpkg2 "net/url"`,
		`"time"`,
		"v1 = args[0].(string)",
		"v2 = args[1].(int64)",
		"v1[v3], err = integer[lib.Level](v4)",
		"v5[lib.Name(v7)] = v9",
		"return lib.Levels(ctx, v1, v5)",
		"var v1 *urlpkg2.URL",
		"if v2, err = integer[time.Duration](args[1])",
		// Only *Name has String, so the pointer is written as it is.
		"if res == nil {\n\t\t\t\treturn nil\n\t\t\t}\n\t\t\treturn writeString(out, res.String())",
		"v1, err = readAll[lib.Name](args[0])",
		"return writeBytes(out, res)",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("commands.go does not contain %q:\n%s", want, src)
		}
	}
}
