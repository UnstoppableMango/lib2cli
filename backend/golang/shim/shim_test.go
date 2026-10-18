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

var (
	str     = typ{kind: "string"}
	num     = typ{kind: "int"}
	boolean = typ{kind: "bool"}
	object  = typ{kind: "object"}
	stream  = typ{kind: "stream"}
)

func list(t typ) typ   { return typ{kind: "list", elem: &t} }
func dict(t typ) typ   { return typ{kind: "map", elem: &t} }
func option(t typ) typ { return typ{kind: "option", elem: &t} }

// commands are written the way the generator writes them.
var commands = []command{
	{
		name:   "greet",
		params: []param{{name: "name", flag: "name", typ: str}, {name: "excited", flag: "excited", typ: boolean}},
		call: func(ctx context.Context, args []any, out io.Writer) error {
			name, excited := Name(args[0].(string)), args[1].(bool)
			s := "Hello, " + string(name)
			if excited {
				s += "!"
			}
			return writeString(out, s)
		},
	},
	{
		name:   "divide",
		params: []param{{name: "a", flag: "a", typ: num}, {name: "b", flag: "b", typ: num}},
		call: func(ctx context.Context, args []any, out io.Writer) error {
			var err error
			var a, b int8
			if a, err = integer[int8](args[0]); err != nil {
				return argError{0, err}
			}
			if b, err = integer[int8](args[1]); err != nil {
				return argError{1, err}
			}
			if b == 0 {
				return errors.New("division by zero")
			}
			return writeJSON(out, a/b)
		},
	},
	{
		name: "tag",
		params: []param{
			{name: "name", flag: "name", typ: str},
			{name: "tags", flag: "tags", typ: list(str)},
			{name: "labels", flag: "labels", typ: dict(str)},
			{name: "note", flag: "note", typ: option(str)},
			{name: "raw_bytes", flag: "raw-bytes", typ: typ{kind: "bytes"}},
		},
		call: func(ctx context.Context, args []any, out io.Writer) error {
			var req TagRequest
			req.Name = Name(args[0].(string))
			if args[1] != nil {
				v1 := args[1].([]any)
				req.Tags = make([]string, len(v1))
				for v2, v3 := range v1 {
					req.Tags[v2] = v3.(string)
				}
			}
			if args[2] != nil {
				v1 := args[2].(map[string]any)
				req.Labels = make(map[string]string, len(v1))
				for k, v := range v1 {
					req.Labels[k] = v.(string)
				}
			}
			if args[3] != nil {
				v := args[3].(string)
				req.Note = &v
			}
			req.RawBytes = args[4].([]byte)
			return writeJSON(out, req)
		},
	},
	{
		name:   "level",
		params: []param{{name: "options", flag: "options", typ: option(object)}, {name: "levels", flag: "levels", typ: list(object)}},
		call: func(ctx context.Context, args []any, out io.Writer) error {
			var err error
			n := 0
			if args[0] != nil {
				var o Options
				if err = decode(args[0], &o); err != nil {
					return argError{0, err}
				}
				n = o.Level
			}
			if args[1] != nil {
				for _, l := range args[1].([]any) {
					var o Options
					if err = decode(l, &o); err != nil {
						return argError{1, err}
					}
					n += o.Level
				}
			}
			return writeJSON(out, n)
		},
	},
	{
		name:   "upper",
		params: []param{{name: "in", flag: "in", typ: stream}},
		call: func(ctx context.Context, args []any, out io.Writer) error {
			b, _ := io.ReadAll(args[0].(io.Reader))
			return writeStream(out, bytes.NewReader(bytes.ToUpper(b)))
		},
	},
	{
		name:   "count",
		params: []param{{name: "in", flag: "in", typ: stream}},
		call: func(ctx context.Context, args []any, out io.Writer) error {
			s, err := readAll[string](args[0])
			if err != nil {
				return argError{0, err}
			}
			return writeJSON(out, strings.Count(s, "\n"))
		},
	},
	{
		name: "context",
		call: func(ctx context.Context, args []any, out io.Writer) error {
			if ctx == nil {
				return errors.New("no context")
			}
			return nil
		},
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
		{name: "json missing field", args: []string{"greet", "--json"}, stdin: `{"excited":true}`, code: exitUsage, err: "name is required"},
		{name: "json null field", args: []string{"greet", "--json"}, stdin: `{"name":null}`, code: exitUsage, err: "name is required"},
		{name: "json wrong type", args: []string{"greet", "--json"}, stdin: `{"name":1}`, code: exitUsage, err: "name number is not a string"},
		{name: "json and flags", args: []string{"greet", "--json", "--name=a"}, code: exitUsage, err: "cannot be combined"},
		{name: "int", args: []string{"divide", "--a=7", "--b=2"}, out: "3\n"},
		{name: "error exits", args: []string{"divide", "--a=7", "--b=0"}, code: exitError, err: "division by zero"},
		{name: "bad int", args: []string{"divide", "--a=x", "--b=1"}, code: exitUsage, err: "--a"},
		{name: "int out of range", args: []string{"divide", "--a=300", "--b=1"}, code: exitUsage, err: "tool divide: --a: 300 is out of range"},
		{name: "json int out of range", args: []string{"divide", "--json"}, stdin: `{"a":1,"b":-200}`, code: exitUsage, err: "tool divide: b: -200 is out of range"},
		{
			name: "whole request",
			args: []string{"tag", "--name=n", "--tags=a", "--tags=b", "--labels=k=v", "--raw-bytes=hi"},
			out:  `{"Name":"n","Tags":["a","b"],"Labels":{"k":"v"},"Note":null,"raw_bytes":"aGk="}` + "\n",
		},
		{
			name:  "whole request from json",
			args:  []string{"tag", "--json"},
			stdin: `{"name":"n","note":"x","raw_bytes":"hi","tags":["a"]}`,
			out:   `{"Name":"n","Tags":["a"],"Labels":null,"Note":"x","raw_bytes":"aGk="}` + "\n",
		},
		{name: "objects", args: []string{"level", `--options={"level":1}`, `--levels={"level":2}`, `--levels={"level":3}`}, out: "6\n"},
		{name: "objects from json", args: []string{"level", "--json"}, stdin: `{"levels":[{"level":2},{"level":3}]}`, out: "5\n"},
		{name: "absent option", args: []string{"level"}, out: "0\n"},
		{name: "bad json", args: []string{"level", "--options={"}, code: exitUsage, err: "not JSON"},
		{name: "object of the wrong shape", args: []string{"level", `--options={"level":"x"}`}, code: exitUsage, err: "--options: json: cannot unmarshal"},
		{name: "stream from stdin", args: []string{"upper", "--in=-"}, stdin: "shout", out: "SHOUT"},
		{name: "stream from file", args: []string{"upper", "--in", file}, out: "A\nB\n"},
		{name: "stream into string", args: []string{"count", "--in", file}, out: "2\n"},
		{name: "stream in json mode", args: []string{"count", "--json"}, stdin: `{"in":"-"}`, code: exitUsage, err: "stdin holds"},
		{name: "context", args: []string{"context"}},
		{name: "no command", code: exitUsage, err: "usage:"},
		{name: "help", args: []string{"--help"}, out: "usage:"},
		{name: "unknown command", args: []string{"nope"}, code: exitUsage, err: `unknown command "nope"`},
		{name: "extra argument", args: []string{"context", "x"}, code: exitUsage, err: "unexpected argument"},
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

func TestInteger(t *testing.T) {
	for _, tt := range []struct {
		n  int64
		ok bool
	}{{127, true}, {128, false}, {-128, true}, {-129, false}} {
		if _, err := integer[int8](tt.n); (err == nil) != tt.ok {
			t.Errorf("int8 %d: got %v", tt.n, err)
		}
	}
	if _, err := integer[uint8](int64(-1)); err == nil {
		t.Error("uint8 -1 converted")
	}
	if n, err := integer[uint64](int64(1 << 62)); err != nil || n != 1<<62 {
		t.Errorf("uint64: got %d, %v", n, err)
	}
}
