package backend

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/exp/golden"
	"github.com/unstoppablemango/tdl/plugin"
)

// TestGo generates the Go project for each fixture bound to a Go module and
// compares go.mod and main.go with testdata/TestGo/<case>.golden. shim.go
// is the runtime copied verbatim, so it is checked against the runtime
// rather than kept in every golden.
func TestGo(t *testing.T) {
	tdl, err := exec.LookPath("tdl")
	if err != nil {
		t.Skip("tdl is not on PATH")
	}

	for _, dir := range []string{"testdata/gofix", "testdata/greet"} {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			files := generate(t, tdl, dir)
			var b bytes.Buffer
			for _, f := range files {
				if f.GetPath() == "shim.go" {
					if string(f.GetContent()) != shimSource() {
						t.Error("shim.go is not the runtime")
					}
					continue
				}
				fmt.Fprintf(&b, "-- %s --\n%s", f.GetPath(), f.GetContent())
			}
			golden.RequireEqual(t, b.Bytes())
		})
	}
}

// TestGoUnexported reports a function name Go cannot call.
func TestGoUnexported(t *testing.T) {
	_, diags := goFiles(&Plan{
		Lang: "go", Package: "example.com/x", Version: "v1.0.0", Bin: "x",
		Commands: []Command{{Name: "f", Func: "_f", Params: []Param{}, Result: Type{Kind: KindString}}},
	})
	if len(diags) != 1 || !strings.Contains(diags[0].GetMessage(), "_f is not an exported Go function name") {
		t.Errorf("got %v", diags)
	}
}

// TestGoBinding reports a binding go.mod could not hold.
func TestGoBinding(t *testing.T) {
	for _, tt := range []struct{ pkg, version, bin, want string }{
		{"example.com/x", "1.0.0", "x", "is not a Go module version"},
		{"example.com/x/v2", "v1.0.0", "x", "is not a Go module version"},
		{"not a path", "v1.0.0", "x", "is not a Go module version"},
		{"example.com/x", "v1.0.0", "my tool", "cannot name a Go binary"},
	} {
		_, diags := goFiles(&Plan{Lang: "go", Package: tt.pkg, Version: tt.version, Bin: tt.bin, Commands: []Command{}})
		if len(diags) != 1 || !strings.Contains(diags[0].GetMessage(), tt.want) {
			t.Errorf("go(%q, %q) bin(%q): got %v, want one diagnostic saying %q", tt.pkg, tt.version, tt.bin, diags, tt.want)
		}
	}
}

// TestGoEndToEnd generates the shim for testdata/greet, builds it against
// the library in testdata/greet/lib, and runs it the way a shell would.
func TestGoEndToEnd(t *testing.T) {
	tdl, err := exec.LookPath("tdl")
	if err != nil {
		t.Skip("tdl is not on PATH")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}

	dir := t.TempDir()
	for _, f := range generate(t, tdl, "testdata/greet") {
		if err := os.WriteFile(filepath.Join(dir, f.GetPath()), f.GetContent(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib, err := filepath.Abs("testdata/greet/lib")
	if err != nil {
		t.Fatal(err)
	}
	mod, err := os.OpenFile(filepath.Join(dir, "go.mod"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(mod, "\nreplace example.com/greet => %s\n", lib)
	mod.Close()

	bin := filepath.Join(dir, "greet")
	build := exec.Command(gobin, "build", "-o", bin, ".")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off", "GOTOOLCHAIN=local", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
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
		{args: []string{"count", "--in", "-"}, stdin: "a\nb\n", out: `{"lines":2,"bytes":4}` + "\n"},
		{args: []string{"upper", "--in", "-"}, stdin: "shout", out: "SHOUT"},
		{args: []string{"tag", "--name", "n", "--tags", "a", "--labels", "k=v", "--note", "x"}, out: `{"label":"n,a,k=v","note":"x"}` + "\n"},
		{args: []string{"version"}, out: "greet v1.0"},
		{args: []string{"greet"}, code: 2, err: "greet greet: --name is required\n"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			cmd := exec.Command(bin, tt.args...)
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

// generate runs the backend on dir/source.tdl and fails on any diagnostic.
func generate(t *testing.T, tdl, dir string) []*plugin.File {
	t.Helper()
	res, err := Backend{}.Generate(context.Background(), &plugin.Request{Target: Target, Model: lower(t, tdl, dir)})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range res.GetDiagnostics() {
		t.Errorf("%s", format(d))
	}
	return res.GetFiles()
}
