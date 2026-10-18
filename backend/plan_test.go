package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/exp/golden"
	"github.com/unstoppablemango/tdl/ir"
	"github.com/unstoppablemango/tdl/plugin"
	"google.golang.org/protobuf/encoding/protojson"
)

// result is what a case's golden file holds: the plan and every diagnostic
// the backend reports, positions written as file:line:col.
type result struct {
	Plan        *Plan    `json:"plan"`
	Diagnostics []string `json:"diagnostics"`
}

// TestPlan lowers each testdata/<case>/source.tdl with tdl, the way tdl gen
// does before calling a plugin, and compares the plan with
// testdata/TestPlan/<case>.golden. go test -update rewrites them.
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

			got := result{Plan: plan, Diagnostics: []string{}}
			for _, d := range diags {
				got.Diagnostics = append(got.Diagnostics, format(d))
			}
			data, err := json.MarshalIndent(got, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			golden.RequireEqual(t, append(data, '\n'))
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

// lower runs tdl ir on dir/source.tdl and decodes the model it prints.
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

// format writes a diagnostic the way tdl prints one.
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
