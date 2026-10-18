// Package shim is the runtime every generated Go shim carries. lib2cli
// copies this file into the generated project as package main, beside
// main.go and the generator that writes commands.go.
//
// The runtime only knows lib2cli's neutral types: it reads each flag, or
// the --json object, into a neutral value and hands the values to a
// command's call. Each call is generated at build time from the
// function's real signature, so the conversions into the library's Go
// types, the context it is given and the error it returns are all written
// out as plain Go and checked by the compiler. Nothing here uses reflect.
package shim

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
)

// typ is a parameter type in lib2cli's neutral set.
type typ struct {
	kind string
	elem *typ
}

// param is one field of a command's request, set by a flag.
type param struct {
	name string
	flag string
	typ  typ
}

// command is one library function, called as a subcommand.
type command struct {
	name   string
	params []param

	// call converts args, one neutral value per param, to the function's
	// types, calls it, and writes its result to out. A neutral value is
	// a string (string, path, text), int64, float64, bool, []byte,
	// io.Reader (stream), json.RawMessage (object), []any (list),
	// map[string]any (map), or nil for an option, list or map left out.
	call func(ctx context.Context, args []any, out io.Writer) error
}

// argError is an argument call could not convert to the function's type.
type argError struct {
	param int
	err   error
}

func (e argError) Error() string { return e.err.Error() }

// Exit codes.
const (
	exitOK    = 0
	exitError = 1 // the function failed
	exitUsage = 2 // the command line was wrong
)

// run executes the command line against cmds and exits. The first SIGINT
// cancels the context a function is given; a second one, which a function
// ignoring its context would otherwise never see, ends the process.
func run(bin string, cmds []command) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	go func() {
		<-ctx.Done()
		stop()
	}()
	code := execute(ctx, bin, cmds, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// execute runs one command line and returns its exit code.
func execute(ctx context.Context, bin string, cmds []command, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(cmds) == 1 {
		return cmds[0].execute(ctx, bin, args, stdin, stdout, stderr)
	}

	if len(args) == 0 {
		usage(stderr, bin, cmds)
		return exitUsage
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage(stdout, bin, cmds)
		return exitOK
	}
	for _, c := range cmds {
		if c.name == args[0] {
			return c.execute(ctx, bin+" "+c.name, args[1:], stdin, stdout, stderr)
		}
	}
	fmt.Fprintf(stderr, "%s: unknown command %q\n", bin, args[0])
	usage(stderr, bin, cmds)
	return exitUsage
}

// usage lists the subcommands.
func usage(w io.Writer, bin string, cmds []command) {
	fmt.Fprintf(w, "usage: %s <command> [flags]\n\ncommands:\n", bin)
	for _, c := range cmds {
		fmt.Fprintf(w, "  %s\n", c.name)
	}
	fmt.Fprintf(w, "\nRun %s <command> --help for a command's flags.\n", bin)
}

// execute parses one command's flags, calls its function, and writes the
// result.
func (c command) execute(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "read the arguments as one JSON object from stdin")
	raw := make([]*values, len(c.params))
	for i, p := range c.params {
		raw[i] = &values{boolean: isBool(p.typ)}
		fs.Var(raw[i], p.flag, describe(p.typ))
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "%s: unexpected argument %q\n", name, fs.Arg(0))
		return exitUsage
	}

	in := &inputs{stdin: stdin}
	defer in.close()

	var vals []any
	var err error
	if *asJSON {
		for i, p := range c.params {
			if len(raw[i].set) > 0 {
				fmt.Fprintf(stderr, "%s: --%s cannot be combined with --json\n", name, p.flag)
				return exitUsage
			}
		}
		vals, err = c.fromJSON(in)
	} else {
		vals, err = c.fromFlags(in, raw)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return exitUsage
	}

	if err := c.call(ctx, vals, stdout); err != nil {
		var arg argError
		if errors.As(err, &arg) {
			label := "--" + c.params[arg.param].flag
			if *asJSON {
				label = c.params[arg.param].name
			}
			fmt.Fprintf(stderr, "%s: %s: %v\n", name, label, arg.err)
			return exitUsage
		}
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return exitError
	}
	return exitOK
}

// values collects every occurrence of one flag.
type values struct {
	set     []string
	boolean bool
}

func (v *values) String() string { return strings.Join(v.set, ",") }

func (v *values) Set(s string) error {
	v.set = append(v.set, s)
	return nil
}

// IsBoolFlag lets a bool flag be given bare, as --verbose.
func (v *values) IsBoolFlag() bool { return v.boolean }

// isBool says whether a flag may be given bare.
func isBool(t typ) bool {
	if t.kind == "option" {
		return isBool(*t.elem)
	}
	return t.kind == "bool"
}

// describe is a flag's help text.
func describe(t typ) string {
	switch t.kind {
	case "list":
		return describe(*t.elem) + ", repeatable"
	case "map":
		return "key=" + describe(*t.elem) + ", repeatable"
	case "option":
		return describe(*t.elem) + ", optional"
	case "object":
		return "JSON object"
	case "stream":
		return "file to read, - for stdin"
	case "path":
		return "file path"
	}
	return t.kind
}

// inputs opens the files stream parameters name and closes them after the
// call.
type inputs struct {
	stdin  io.Reader
	opened []io.Closer
}

func (in *inputs) open(name string, allowStdin bool) (io.Reader, error) {
	if name == "-" {
		if !allowStdin {
			return nil, errors.New("stdin holds the --json arguments, so a stream cannot read it")
		}
		return in.stdin, nil
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	in.opened = append(in.opened, f)
	return f, nil
}

func (in *inputs) close() {
	for _, c := range in.opened {
		c.Close()
	}
}

// fromFlags converts each flag's occurrences to a neutral value. A missing
// bool is false and a missing option, list or map is nil; any other
// missing flag is an error.
func (c command) fromFlags(in *inputs, raw []*values) ([]any, error) {
	vals := make([]any, len(c.params))
	for i, p := range c.params {
		set := raw[i].set
		switch {
		case p.typ.kind == "list" && len(set) > 0:
			list := make([]any, 0, len(set))
			for _, s := range set {
				v, err := parse(in, *p.typ.elem, s)
				if err != nil {
					return nil, fmt.Errorf("--%s: %w", p.flag, err)
				}
				list = append(list, v)
			}
			vals[i] = list
		case p.typ.kind == "map" && len(set) > 0:
			m := make(map[string]any, len(set))
			for _, s := range set {
				k, rest, ok := strings.Cut(s, "=")
				if !ok {
					return nil, fmt.Errorf("--%s: %q is not key=value", p.flag, s)
				}
				v, err := parse(in, *p.typ.elem, rest)
				if err != nil {
					return nil, fmt.Errorf("--%s: %w", p.flag, err)
				}
				m[k] = v
			}
			vals[i] = m
		case len(set) > 1:
			return nil, fmt.Errorf("--%s is given more than once", p.flag)
		case len(set) == 1:
			v, err := parse(in, p.typ, set[0])
			if err != nil {
				return nil, fmt.Errorf("--%s: %w", p.flag, err)
			}
			vals[i] = v
		default:
			v, err := absent(p.typ)
			if err != nil {
				return nil, fmt.Errorf("--%s %w", p.flag, err)
			}
			vals[i] = v
		}
	}
	return vals, nil
}

// absent is the value of a parameter left out: false for a bool, nil for
// an option, list or map, and an error for anything else.
func absent(t typ) (any, error) {
	switch t.kind {
	case "bool":
		return false, nil
	case "option", "list", "map":
		return nil, nil
	}
	return nil, errors.New("is required")
}

// parse converts one flag occurrence. A list or map inside a list or map
// is given as JSON.
func parse(in *inputs, t typ, s string) (any, error) {
	switch t.kind {
	case "option":
		return parse(in, *t.elem, s)
	case "int":
		return strconv.ParseInt(s, 10, 64)
	case "float":
		return strconv.ParseFloat(s, 64)
	case "bool":
		return strconv.ParseBool(s)
	case "bytes":
		return []byte(s), nil
	case "stream":
		return in.open(s, true)
	case "object", "list", "map":
		if !json.Valid([]byte(s)) {
			return nil, fmt.Errorf("%q is not JSON", s)
		}
		return fromRaw(in, t, json.RawMessage(s))
	}
	return s, nil
}

// fromJSON reads the arguments as one object from stdin, keyed by field
// name. As with flags, a missing or null bool, option, list or map field
// takes its absent value, and any other is an error.
func (c command) fromJSON(in *inputs) ([]any, error) {
	var obj map[string]json.RawMessage
	if err := json.NewDecoder(in.stdin).Decode(&obj); err != nil {
		return nil, fmt.Errorf("reading --json arguments: %w", err)
	}
	vals := make([]any, len(c.params))
	for i, p := range c.params {
		var err error
		if data, ok := obj[p.name]; ok && string(data) != "null" {
			vals[i], err = fromRaw(in, p.typ, data)
		} else {
			vals[i], err = absent(p.typ)
		}
		if err != nil {
			return nil, fmt.Errorf("%s %w", p.name, err)
		}
	}
	return vals, nil
}

// fromRaw converts a JSON value to a neutral value. A stream is opened
// from the path it names, and bytes are the string's bytes.
func fromRaw(in *inputs, t typ, data json.RawMessage) (any, error) {
	var err error
	switch t.kind {
	case "option":
		if string(data) == "null" {
			return nil, nil
		}
		return fromRaw(in, *t.elem, data)
	case "int":
		var n int64
		err = json.Unmarshal(data, &n)
		return n, wrap(err)
	case "float":
		var f float64
		err = json.Unmarshal(data, &f)
		return f, wrap(err)
	case "bool":
		var b bool
		err = json.Unmarshal(data, &b)
		return b, wrap(err)
	case "object":
		return data, nil
	case "list":
		var raw []json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, wrap(err)
		}
		list := make([]any, len(raw))
		for i, r := range raw {
			if list[i], err = fromRaw(in, *t.elem, r); err != nil {
				return nil, err
			}
		}
		return list, nil
	case "map":
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, wrap(err)
		}
		m := make(map[string]any, len(raw))
		for k, r := range raw {
			if m[k], err = fromRaw(in, *t.elem, r); err != nil {
				return nil, err
			}
		}
		return m, nil
	}

	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, wrap(err)
	}
	switch t.kind {
	case "bytes":
		return []byte(s), nil
	case "stream":
		return in.open(s, false)
	}
	return s, nil
}

// wrap says which JSON value was the wrong type.
func wrap(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return fmt.Errorf("%s is not a %s", typeErr.Value, typeErr.Type)
	}
	return err
}

// The helpers below are what generated calls convert and write with.

// integer converts an int argument to the function's integer type,
// rejecting a value the type cannot hold.
func integer[T ~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr](v any) (T, error) {
	n := v.(int64)
	t := T(n)
	if int64(t) != n || (t < 0) != (n < 0) {
		return 0, fmt.Errorf("%d is out of range", n)
	}
	return t, nil
}

// readAll reads a stream argument whole, for a function taking a string
// or byte slice.
func readAll[T ~string | ~[]byte](v any) (T, error) {
	data, err := io.ReadAll(v.(io.Reader))
	return T(data), err
}

// decode decodes an object argument into p.
func decode(v any, p any) error {
	return json.Unmarshal(v.(json.RawMessage), p)
}

func writeString(w io.Writer, s string) error {
	_, err := io.WriteString(w, s)
	return err
}

func writeBytes(w io.Writer, b []byte) error {
	_, err := w.Write(b)
	return err
}

// writeStream copies a stream result, closing it after when it can be.
func writeStream(w io.Writer, r io.Reader) error {
	if c, ok := r.(io.Closer); ok {
		defer c.Close()
	}
	_, err := io.Copy(w, r)
	return err
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
