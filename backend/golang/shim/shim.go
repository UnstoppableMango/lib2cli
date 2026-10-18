// Package shim is the runtime every generated Go shim carries. lib2cli
// copies this file into the generated project as package main, beside
// main.go and the generator that writes commands.go.
//
// The runtime only knows lib2cli's neutral types. It turns a command's
// flags into one JSON object keyed by parameter name, or takes that object
// from stdin with --json, and hands it to the command's call. Each call is
// generated at build time from the function's real signature: it decodes
// the object with encoding/json into a struct of the function's own
// parameter types, calls the function, and writes the result. Nothing here
// uses reflect.
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

	// call decodes args, a JSON object keyed by parameter name, into the
	// function's types, calls it, and writes its result to out. It opens
	// stream parameters from in.
	call func(ctx context.Context, args []byte, in *inputs, out io.Writer) error
}

// argError is an argument call could not use: parameter param's, or
// with param -1, the object's.
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

	var obj []byte
	var err error
	if *asJSON {
		for i, p := range c.params {
			if len(raw[i].set) > 0 {
				fmt.Fprintf(stderr, "%s: --%s cannot be combined with --json\n", name, p.flag)
				return exitUsage
			}
		}
		in.stdinRead = true
		obj, err = c.fromJSON(stdin)
	} else {
		obj, err = c.fromFlags(raw)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return exitUsage
	}

	err = c.call(ctx, obj, in, stdout)
	var arg argError
	switch {
	case err == nil:
		return exitOK
	case errors.As(err, &arg):
		fmt.Fprintf(stderr, "%s: %s\n", name, c.describeArg(arg, *asJSON))
		return exitUsage
	default:
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return exitError
	}
}

// describeArg says which parameter an argError is about, as its flag or,
// with --json, its field.
func (c command) describeArg(e argError, asJSON bool) string {
	i, msg := e.param, e.err.Error()
	var typeErr *json.UnmarshalTypeError
	if errors.As(e.err, &typeErr) {
		field, _, _ := strings.Cut(typeErr.Field, ".")
		for j, p := range c.params {
			if p.name == field {
				i = j
			}
		}
		msg = fmt.Sprintf("%s is not a valid %s", typeErr.Value, typeErr.Type)
	}
	switch {
	case i < 0:
		return msg
	case asJSON:
		return c.params[i].name + ": " + msg
	}
	return "--" + c.params[i].flag + ": " + msg
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
	stdin     io.Reader
	stdinRead bool // stdin held the --json arguments
	opened    []io.Closer
}

// open opens a stream parameter: the file name names, or stdin for -.
func (in *inputs) open(name string) (io.Reader, error) {
	if name == "-" {
		if in.stdinRead {
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

// fromFlags writes the flags as the JSON object a call decodes. A missing
// bool, option, list or map is left out, so it decodes as its zero value;
// any other missing flag is an error.
func (c command) fromFlags(raw []*values) ([]byte, error) {
	obj := make(map[string]any, len(c.params))
	for i, p := range c.params {
		set := raw[i].set
		var v any
		var err error
		switch {
		case len(set) == 0:
			if !omittable(p.typ) {
				return nil, fmt.Errorf("--%s is required", p.flag)
			}
			continue
		case p.typ.kind == "list" || p.typ.kind == "map":
			v, err = repeated(p.typ, set)
		case len(set) > 1:
			return nil, fmt.Errorf("--%s is given more than once", p.flag)
		default:
			v, err = literal(p.typ, set[0])
		}
		if err != nil {
			return nil, fmt.Errorf("--%s: %w", p.flag, err)
		}
		obj[p.name] = v
	}
	return json.Marshal(obj)
}

// repeated writes every occurrence of a list flag as a JSON array, or of
// a map flag, each key=value, as an object.
func repeated(t typ, set []string) (any, error) {
	list := make([]json.RawMessage, len(set))
	m := make(map[string]json.RawMessage, len(set))
	for i, s := range set {
		var k string
		if t.kind == "map" {
			var ok bool
			if k, s, ok = strings.Cut(s, "="); !ok {
				return nil, fmt.Errorf("%q is not key=value", k)
			}
		}
		v, err := literal(*t.elem, s)
		if err != nil {
			return nil, err
		}
		list[i], m[k] = v, v
	}
	if t.kind == "map" {
		return m, nil
	}
	return list, nil
}

// literal writes one flag occurrence as JSON. A list, map or object is
// given as JSON already; a stream is the name of the file to open.
func literal(t typ, s string) (json.RawMessage, error) {
	switch t.kind {
	case "option":
		return literal(*t.elem, s)
	case "int":
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not an integer", s)
		}
		return json.RawMessage(strconv.FormatInt(n, 10)), nil
	case "float":
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", s)
		}
		return json.Marshal(f)
	case "bool":
		b, err := strconv.ParseBool(s)
		if err != nil {
			return nil, fmt.Errorf("%q is not a bool", s)
		}
		return json.Marshal(b)
	case "object", "list", "map":
		if !json.Valid([]byte(s)) {
			return nil, fmt.Errorf("%q is not JSON", s)
		}
		return json.RawMessage(s), nil
	}
	return json.Marshal(s)
}

// omittable says whether a parameter may be left out.
func omittable(t typ) bool {
	switch t.kind {
	case "option", "bool", "list", "map":
		return true
	}
	return false
}

// fromJSON reads the arguments as one object from stdin. As with flags, a
// missing or null bool, option, list or map field decodes as its zero
// value, and any other is an error.
func (c command) fromJSON(stdin io.Reader) ([]byte, error) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return nil, fmt.Errorf("reading --json arguments: %w", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("reading --json arguments: %w", err)
	}
	for _, p := range c.params {
		if v, ok := obj[p.name]; (!ok || string(v) == "null") && !omittable(p.typ) {
			return nil, fmt.Errorf("%s is required", p.name)
		}
	}
	return data, nil
}

// The helpers below are what generated calls decode and write with.

// decode decodes a call's arguments into req.
func decode(args []byte, req any) error {
	if err := json.Unmarshal(args, req); err != nil {
		return argError{-1, err}
	}
	return nil
}

// readAll reads a stream argument whole, for a function taking a string
// or byte slice.
func readAll[T ~string | ~[]byte](r io.Reader) (T, error) {
	data, err := io.ReadAll(r)
	return T(data), err
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
