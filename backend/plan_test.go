package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/unstoppablemango/tdl/ir"
	"github.com/unstoppablemango/tdl/plugin"
	"google.golang.org/protobuf/encoding/protojson"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// golden is what a case's plan.json holds: the plan and every diagnostic
// the backend reports, positions written as file:line:col.
type golden struct {
	Plan        *Plan    `json:"plan"`
	Diagnostics []string `json:"diagnostics"`
}

// TestPlan lowers each testdata/<case>/source.tdl with tdl, the way tdl gen
// does before calling a plugin, and compares the plan with plan.json.
func TestPlan(t *testing.T) {
	tdl, err := exec.LookPath("tdl")
	if err != nil {
		t.Skip("tdl is not on PATH")
	}

	cases, err := filepath.Glob("testdata/*/source.tdl")
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range cases {
		dir := filepath.Dir(src)
		t.Run(filepath.Base(dir), func(t *testing.T) {
			req := &plugin.Request{Target: Target, Model: lower(t, tdl, dir)}
			plan, diags := Build(req)

			got := golden{Plan: plan, Diagnostics: []string{}}
			for _, d := range diags {
				got.Diagnostics = append(got.Diagnostics, format(d))
			}
			data, err := json.MarshalIndent(got, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			data = append(data, '\n')

			path := filepath.Join(dir, "plan.json")
			if *update {
				if err := os.WriteFile(path, data, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run go test -update to create it)", err)
			}
			if !bytes.Equal(data, want) {
				t.Errorf("plan differs from %s (run go test -update to accept):\n%s", path, data)
			}
		})
	}
}

// TestGenerate checks that a model that plans cleanly says code generation
// is pending, and that one with errors says nothing more.
func TestGenerate(t *testing.T) {
	tdl, err := exec.LookPath("tdl")
	if err != nil {
		t.Skip("tdl is not on PATH")
	}

	for dir, want := range map[string]int{"testdata/mdx": 1, "testdata/nobinding": 1} {
		res, err := Backend{}.Generate(context.Background(), &plugin.Request{
			Target: Target,
			Model:  lower(t, tdl, dir),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.GetFiles()) != 0 || len(res.GetDiagnostics()) != want {
			t.Errorf("%s: got %d files and %d diagnostics, want none and %d", dir, len(res.GetFiles()), len(res.GetDiagnostics()), want)
		}
	}
}

func lower(t *testing.T, tdl, dir string) *ir.Model {
	t.Helper()
	cmd := exec.Command(tdl, "ir", "--format", "json", "source.tdl")
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("tdl ir: %v", err)
	}
	model := &ir.Model{}
	if err := protojson.Unmarshal(out, model); err != nil {
		t.Fatal(err)
	}
	return model
}

func format(d *plugin.Diagnostic) string {
	sev := "error"
	if d.GetSeverity() == plugin.Severity_SEVERITY_WARNING {
		sev = "warning"
	}
	p := d.GetPosition()
	if p == nil {
		return fmt.Sprintf("%s: %s", sev, d.GetMessage())
	}
	return fmt.Sprintf("%s:%d:%d: %s: %s", p.GetFilename(), p.GetLine(), p.GetColumn(), sev, d.GetMessage())
}
