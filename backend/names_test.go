package backend

import "testing"

func TestKebab(t *testing.T) {
	for in, want := range map[string]string{
		"compile":      "compile",
		"convertFile":  "convert-file",
		"raw_bytes":    "raw-bytes",
		"HTTPServer":   "httpserver",
		"toV2Format":   "to-v2-format",
		"outputFormat": "output-format",
	} {
		if got := kebab(in); got != want {
			t.Errorf("kebab(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultBin(t *testing.T) {
	for in, want := range map[string]string{
		"@mdx-js/mdx":                 "mdx",
		"left-pad":                    "left-pad",
		"github.com/example/gofix/v2": "gofix",
		"github.com/example/tool":     "tool",
	} {
		if got := defaultBin(in); got != want {
			t.Errorf("defaultBin(%q) = %q, want %q", in, got, want)
		}
	}
}
