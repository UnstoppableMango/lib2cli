package shim

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type Name string

type Options struct {
	Level int `json:"level"`
}

type TagRequest struct {
	Name     Name
	Tags     []string
	Labels   map[string]string
	Note     *string
	RawBytes []byte `json:"raw_bytes"`
}

type version struct{ major, minor int }

func (v version) String() string {
	return "v" + string(rune('0'+v.major)) + "." + string(rune('0'+v.minor))
}

var (
	str     = typ{kind: "string"}
	integer = typ{kind: "int"}
	boolean = typ{kind: "bool"}
	object  = typ{kind: "object"}
	stream  = typ{kind: "stream"}
)

func list(t typ) typ   { return typ{kind: "list", elem: &t} }
func dict(t typ) typ   { return typ{kind: "map", elem: &t} }
func option(t typ) typ { return typ{kind: "option", elem: &t} }

var commands = []command{
	{
		name: "greet",
		fn: func(name Name, excited bool) string {
			if excited {
				return "Hello, " + string(name) + "!"
			}
			return "Hello, " + string(name)
		},
		params: []param{{name: "name", flag: "name", typ: str}, {name: "excited", flag: "excited", typ: boolean}},
		result: str,
	},
	{
		name: "divide",
		fn: func(ctx context.Context, a, b int) (int, error) {
			if ctx == nil {
				return 0, errors.New("no context")
			}
			if b == 0 {
				return 0, errors.New("division by zero")
			}
			return a / b, nil
		},
		params: []param{{name: "a", flag: "a", typ: integer}, {name: "b", flag: "b", typ: integer}},
		result: integer,
	},
	{
		name: "tag",
		fn: func(r *TagRequest) TagRequest {
			return *r
		},
		whole: true,
		params: []param{
			{name: "name", flag: "name", typ: str},
			{name: "tags", flag: "tags", typ: list(str)},
			{name: "labels", flag: "labels", typ: dict(str)},
			{name: "note", flag: "note", typ: option(str)},
			{name: "raw_bytes", flag: "raw-bytes", typ: typ{kind: "bytes"}},
		},
		result: object,
	},
	{
		name: "level",
		fn: func(o *Options, levels []Options) int {
			n := 0
			if o != nil {
				n = o.Level
			}
			for _, l := range levels {
				n += l.Level
			}
			return n
		},
		params: []param{{name: "options", flag: "options", typ: option(object)}, {name: "levels", flag: "levels", typ: list(object)}},
		result: integer,
	},
	{
		name:   "upper",
		fn:     func(in io.Reader) io.Reader { b, _ := io.ReadAll(in); return bytes.NewReader(bytes.ToUpper(b)) },
		params: []param{{name: "in", flag: "in", typ: stream}},
		result: stream,
	},
	{
		name:   "count",
		fn:     func(in string) int { return strings.Count(in, "\n") },
		params: []param{{name: "in", flag: "in", typ: stream}},
		result: integer,
	},
	{
		name:   "version",
		fn:     func() version { return version{1, 2} },
		result: typ{kind: "text"},
	},
	{
		name:   "nothing",
		fn:     func() error { return nil },
		result: object,
	},
}

func TestExecute(t *testing.T) {
	file := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(file, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		args  []string
		stdin string
		code  int
		out   string
		err   string
	}{
		{name: "raw string", args: []string{"greet", "--name", "Ada"}, out: "Hello, Ada"},
		{name: "bare bool", args: []string{"greet", "--name=Ada", "--excited"}, out: "Hello, Ada!"},
		{name: "json mode", args: []string{"greet", "--json"}, stdin: `{"name":"Ada","excited":true}`, out: "Hello, Ada!"},
		{name: "missing flag", args: []string{"greet"}, code: exitUsage, err: "--name is required"},
		{name: "repeated scalar", args: []string{"greet", "--name=a", "--name=b"}, code: exitUsage, err: "more than once"},
		{name: "json and flags", args: []string{"greet", "--json", "--name=a"}, code: exitUsage, err: "cannot be combined"},
		{name: "context and error", args: []string{"divide", "--a=7", "--b=2"}, out: "3\n"},
		{name: "error exits", args: []string{"divide", "--a=7", "--b=0"}, code: exitError, err: "division by zero"},
		{name: "bad int", args: []string{"divide", "--a=x", "--b=1"}, code: exitUsage, err: "--a"},
		{
			name: "whole request",
			args: []string{"tag", "--name=n", "--tags=a", "--tags=b", "--labels=k=v", "--raw-bytes=hi"},
			out:  `{"Name":"n","Tags":["a","b"],"Labels":{"k":"v"},"Note":null,"raw_bytes":"aGk="}` + "\n",
		},
		{
			name:  "whole request from json",
			args:  []string{"tag", "--json"},
			stdin: `{"name":"n","note":"x","raw_bytes":"hi"}`,
			out:   `{"Name":"n","Tags":null,"Labels":null,"Note":"x","raw_bytes":"aGk="}` + "\n",
		},
		{name: "objects", args: []string{"level", `--options={"level":1}`, `--levels={"level":2}`, `--levels={"level":3}`}, out: "6\n"},
		{name: "absent option", args: []string{"level"}, out: "0\n"},
		{name: "bad json", args: []string{"level", "--options={"}, code: exitUsage, err: "not JSON"},
		{name: "stream from stdin", args: []string{"upper", "--in=-"}, stdin: "shout", out: "SHOUT"},
		{name: "stream from file", args: []string{"upper", "--in", file}, out: "A\nB\n"},
		{name: "stream into string", args: []string{"count", "--in", file}, out: "2\n"},
		{name: "stream in json mode", args: []string{"count", "--json"}, stdin: `{"in":"-"}`, code: exitUsage, err: "stdin holds"},
		{name: "text", args: []string{"version"}, out: "v1.2"},
		{name: "no result", args: []string{"nothing"}},
		{name: "no command", code: exitUsage, err: "usage:"},
		{name: "help", args: []string{"--help"}, out: "usage:"},
		{name: "unknown command", args: []string{"nope"}, code: exitUsage, err: `unknown command "nope"`},
		{name: "extra argument", args: []string{"version", "x"}, code: exitUsage, err: "unexpected argument"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := execute(context.Background(), "tool", commands, tt.args, strings.NewReader(tt.stdin), &out, &errOut)
			if code != tt.code {
				t.Errorf("exit code %d, want %d; stderr: %s", code, tt.code, errOut.String())
			}
			if tt.out != "" && !strings.HasPrefix(out.String(), tt.out) || tt.out == "" && out.Len() > 0 && tt.code == 0 {
				t.Errorf("stdout %q, want %q", out.String(), tt.out)
			}
			if !strings.Contains(errOut.String(), tt.err) {
				t.Errorf("stderr %q, want it to contain %q", errOut.String(), tt.err)
			}
		})
	}
}

func TestSingleCommand(t *testing.T) {
	var out bytes.Buffer
	code := execute(context.Background(), "greet", commands[:1], []string{"--name", "Ada"}, strings.NewReader(""), &out, io.Discard)
	if code != exitOK || out.String() != "Hello, Ada" {
		t.Errorf("got %d %q, want a greeting without a subcommand", code, out.String())
	}
}

func TestSignatureMismatch(t *testing.T) {
	cmds := []command{{name: "f", fn: func(a, b int) int { return a + b }, params: []param{{name: "a", flag: "a", typ: integer}}, result: integer}}
	var errOut bytes.Buffer
	code := execute(context.Background(), "f", cmds, []string{"--a=1"}, strings.NewReader(""), io.Discard, &errOut)
	if code != exitError || !strings.Contains(errOut.String(), "takes 2 argument(s), but the model gives it 1") {
		t.Errorf("got %d %q", code, errOut.String())
	}
}
