package backend

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/exp/golden"
)

// TestTS generates the TypeScript project for each fixture bound to an npm
// package and compares package.json, tsconfig.json and src/main.ts with
// testdata/TestTS/<case>.golden. src/shim.ts is copied verbatim, so it is
// checked against its source rather than kept in every golden.
func TestTS(t *testing.T) {
	tdl, err := exec.LookPath("tdl")
	if err != nil {
		t.Skip("tdl is not on PATH")
	}

	for _, dir := range []string{"testdata/greetts", "testdata/mdx"} {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			var b bytes.Buffer
			for _, f := range generate(t, tdl, dir) {
				if f.GetPath() == "src/shim.ts" {
					if string(f.GetContent()) != tsHeader+tsShim(t) {
						t.Error("src/shim.ts is not the runtime")
					}
					continue
				}
				fmt.Fprintf(&b, "-- %s --\n%s", f.GetPath(), f.GetContent())
			}
			golden.RequireEqual(t, b.Bytes())
		})
	}
}

func tsShim(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("typescript/shim/shim.ts")
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}

// TestTSBinding reports a binding or function name package.json and
// main.ts could not hold.
func TestTSBinding(t *testing.T) {
	for _, tt := range []struct{ pkg, version, bin, fn, want string }{
		{"Greet", "1.0.0", "greet", "f", "is not an npm package name"},
		{"greet", "^1.0.0", "greet", "f", "is not an exact version"},
		{"greet", "1.0", "greet", "f", "is not an exact version"},
		{"greet", "1.0.0", "my tool", "f", "cannot name an npm binary"},
		{"greet", "1.0.0", "greet", "convert-file", "convert-file is not a JavaScript function name"},
	} {
		_, diags := tsFiles(&Plan{
			Lang: "ts", Package: tt.pkg, Version: tt.version, Bin: tt.bin,
			Commands: []Command{{Name: "f", Func: tt.fn, Params: []Param{}, Result: Type{Kind: KindString}}},
		})
		if len(diags) != 1 || !strings.Contains(diags[0].GetMessage(), tt.want) {
			t.Errorf("npm(%q, %q) bin(%q) func(%q): got %v, want one diagnostic saying %q", tt.pkg, tt.version, tt.bin, tt.fn, diags, tt.want)
		}
	}
}

// TestTSEndToEnd generates the project for testdata/greetts, builds it
// with tsc against the library in testdata/greetts/lib, and runs the shim
// the way a shell would. tsc and Node's types come from the
// node_modules LIB2CLI_NODE_MODULES names, by default the one npm install
// writes in backend/typescript.
func TestTSEndToEnd(t *testing.T) {
	tdl, err := exec.LookPath("tdl")
	if err != nil {
		t.Skip("tdl is not on PATH")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}
	deps := os.Getenv("LIB2CLI_NODE_MODULES")
	if deps == "" {
		deps = "typescript/node_modules"
	}
	if deps, err = filepath.Abs(deps); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(deps, "typescript")); err != nil {
		t.Skipf("no typescript in %s: run npm install in backend/typescript", deps)
	}

	dir := t.TempDir()
	for _, f := range generate(t, tdl, "testdata/greetts") {
		path := filepath.Join(dir, f.GetPath())
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, f.GetContent(), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// node_modules is what npm install would write: the library, tsc and
	// Node's types.
	modules := filepath.Join(dir, "node_modules")
	if err := os.Mkdir(modules, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(deps)
	if err != nil {
		t.Fatal(err)
	}
	links := map[string]string{"greet": "testdata/greetts/lib"}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			links[e.Name()] = filepath.Join(deps, e.Name())
		}
	}
	for name, target := range links {
		if target, err = filepath.Abs(target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(modules, name)); err != nil {
			t.Fatal(err)
		}
	}

	build := exec.Command(node, filepath.Join(modules, "typescript", "bin", "tsc"))
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("tsc: %v\n%s", err, out)
	}

	tests := []struct {
		args  []string
		stdin string
		code  int
		out   string
		err   string
	}{
		{args: []string{"greet", "--name", "Ada", "--excited"}, out: "Hello, Ada!"},
		{args: []string{"divide", "--a", "9", "--b", "3"}, out: "3\n"},
		{args: []string{"divide", "--json"}, stdin: `{"a": 1, "b": 4}`, out: "0.25\n"},
		{args: []string{"divide", "--a", "1", "--b", "0"}, code: 1, err: "greet divide: division by zero\n"},
		{args: []string{"divide", "--a", "x", "--b", "0"}, code: 2, err: "greet divide: --a: \"x\" is not a number\n"},
		{args: []string{"count", "--in", "-"}, stdin: "a\nb\n", out: `{"lines":2,"bytes":4}` + "\n"},
		{args: []string{"upper", "--in", "-"}, stdin: "shout", out: "SHOUT"},
		{args: []string{"tag", "--name", "n", "--tags", "a", "--labels", "k=v", "--note", "x"}, out: `{"label":"n,a,k=v","note":"x"}` + "\n"},
		{args: []string{"tag", "--name", "n", `--options={"shout":true}`}, out: `{"label":"N"}` + "\n"},
		{args: []string{"version"}, out: "greet v1.0"},
		{args: []string{"greet"}, code: 2, err: "greet greet: --name is required\n"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			cmd := exec.Command(node, append([]string{filepath.Join(dir, "dist", "main.js")}, tt.args...)...)
			cmd.Stdin = strings.NewReader(tt.stdin)
			var out, errOut bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &errOut
			err := cmd.Run()
			code := 0
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if code != tt.code || out.String() != tt.out || errOut.String() != tt.err {
				t.Errorf("got exit %d, stdout %q, stderr %q; want %d, %q, %q", code, out.String(), errOut.String(), tt.code, tt.out, tt.err)
			}
		})
	}
}
