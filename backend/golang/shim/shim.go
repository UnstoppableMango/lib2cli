// Package shim is the runtime every generated Go shim carries. lib2cli
// copies this file into the generated project as package main, beside a
// main.go holding the command table, so the shim depends only on the
// library it wraps and the standard library.
//
// The table says what the TDL model declares. Everything the model leaves
// to the language is read from the function's own signature when the shim
// runs: an injected context.Context, an error return setting the exit
// code, and the Go types each value decodes into.
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
	"reflect"
	"strconv"
	"strings"
)

// typ is a parameter or result type in lib2cli's neutral set.
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
	name string
	fn   any

	// whole passes the request as one struct rather than its fields as
	// positional arguments.
	whole bool

	params []param
	result typ
}

// Exit codes.
const (
	exitOK    = 0
	exitError = 1 // the function failed, or its signature did not fit
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

	out, err := c.call(ctx, vals)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return exitError
	}
	if out.IsValid() {
		if err := write(stdout, c.result, out); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", name, err)
			return exitError
		}
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

// fromFlags converts each flag's occurrences to a value. A missing
// optional, list or map flag, or a missing bool, is its zero value; any
// other missing flag is an error.
func (c command) fromFlags(in *inputs, raw []*values) ([]any, error) {
	vals := make([]any, len(c.params))
	for i, p := range c.params {
		set := raw[i].set
		switch {
		case p.typ.kind == "list":
			list := make([]any, 0, len(set))
			for _, s := range set {
				v, err := parse(in, *p.typ.elem, s)
				if err != nil {
					return nil, fmt.Errorf("--%s: %w", p.flag, err)
				}
				list = append(list, v)
			}
			vals[i] = list
		case p.typ.kind == "map":
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
		case !omittable(p.typ):
			return nil, fmt.Errorf("--%s is required", p.flag)
		}
	}
	return vals, nil
}

// omittable says whether a parameter may be left out.
func omittable(t typ) bool {
	switch t.kind {
	case "option", "bool", "list", "map":
		return true
	}
	return false
}

// parse converts one flag occurrence.
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
		return json.RawMessage(s), nil
	}
	return s, nil
}

// fromJSON reads the arguments as one object from stdin, keyed by field
// name. As with flags, a missing or null optional, list, map or bool field
// is its zero value, and any other is an error.
func (c command) fromJSON(in *inputs) ([]any, error) {
	var obj map[string]json.RawMessage
	if err := json.NewDecoder(in.stdin).Decode(&obj); err != nil {
		return nil, fmt.Errorf("reading --json arguments: %w", err)
	}
	vals := make([]any, len(c.params))
	for i, p := range c.params {
		data, ok := obj[p.name]
		if !ok || string(data) == "null" {
			if !omittable(p.typ) {
				return nil, fmt.Errorf("%s is required", p.name)
			}
			continue
		}
		v, err := fromRaw(in, p.typ, data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.name, err)
		}
		vals[i] = v
	}
	return vals, nil
}

// fromRaw converts a JSON value where JSON alone cannot: a stream is
// opened from the path it names, and bytes are the string's bytes.
func fromRaw(in *inputs, t typ, data json.RawMessage) (any, error) {
	switch t.kind {
	case "option":
		return fromRaw(in, *t.elem, data)
	case "stream", "bytes":
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return nil, err
		}
		if t.kind == "bytes" {
			return []byte(s), nil
		}
		return in.open(s, false)
	}
	return data, nil
}

var (
	contextType = reflect.TypeFor[context.Context]()
	errorType   = reflect.TypeFor[error]()
)

// call calls the function with vals. It injects ctx when the function's
// first parameter is a context.Context, and returns the function's error
// when its last result is one. The returned value is invalid when the
// function returns nothing else.
func (c command) call(ctx context.Context, vals []any) (reflect.Value, error) {
	fn := reflect.ValueOf(c.fn)
	ft := fn.Type()
	if ft.IsVariadic() {
		return reflect.Value{}, errors.New("variadic functions are not supported")
	}

	var ins []reflect.Value
	first := 0
	if ft.NumIn() > 0 && ft.In(0) == contextType {
		ins = append(ins, reflect.ValueOf(ctx))
		first = 1
	}

	want := len(vals)
	if c.whole {
		want = 1
	}
	if ft.NumIn()-first != want {
		return reflect.Value{}, fmt.Errorf("the function takes %d argument(s), but the model gives it %d", ft.NumIn()-first, want)
	}

	if c.whole {
		v, err := c.request(ft.In(first), vals)
		if err != nil {
			return reflect.Value{}, err
		}
		ins = append(ins, v)
	} else {
		for i, p := range c.params {
			v, err := convert(ft.In(first+i), vals[i])
			if err != nil {
				return reflect.Value{}, fmt.Errorf("%s: %w", p.name, err)
			}
			ins = append(ins, v)
		}
	}

	outs := ft.NumOut()
	if outs > 0 && ft.Out(outs-1) == errorType {
		outs--
	}
	if outs > 1 {
		return reflect.Value{}, fmt.Errorf("the function returns %d values; a command writes one", outs)
	}

	res := fn.Call(ins)
	if outs < len(res) {
		if err := res[outs]; !err.IsNil() {
			return reflect.Value{}, err.Interface().(error)
		}
	}
	if outs == 0 {
		return reflect.Value{}, nil
	}
	return res[0], nil
}

// request builds the struct a whole command passes. Each parameter sets
// the field whose json tag is its name or, without a tag, whose name
// matches it ignoring case and underscores.
func (c command) request(t reflect.Type, vals []any) (reflect.Value, error) {
	st := t
	if st.Kind() == reflect.Pointer {
		st = st.Elem()
	}
	if st.Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("the function takes %s, but a whole command passes a struct", t)
	}

	req := reflect.New(st)
	for i, p := range c.params {
		f, ok := field(st, p.name)
		if !ok {
			return reflect.Value{}, fmt.Errorf("%s has no field for %s", st, p.name)
		}
		v, err := convert(f.Type, vals[i])
		if err != nil {
			return reflect.Value{}, fmt.Errorf("%s: %w", p.name, err)
		}
		req.Elem().FieldByIndex(f.Index).Set(v)
	}
	if t.Kind() == reflect.Pointer {
		return req, nil
	}
	return req.Elem(), nil
}

// field finds the exported field of st that name sets.
func field(st reflect.Type, name string) (reflect.StructField, bool) {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }
	for _, f := range reflect.VisibleFields(st) {
		if !f.IsExported() {
			continue
		}
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == name || (tag == "" && norm(f.Name) == norm(name)) {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

// convert makes v a value of t. A value already assignable is used as it
// is, bytes convert to any byte slice type, and a stream is read whole
// into a string or byte slice. Anything else decodes from its JSON.
func convert(t reflect.Type, v any) (reflect.Value, error) {
	if v == nil {
		return reflect.Zero(t), nil
	}
	rv := reflect.ValueOf(v)
	if rv.Type().AssignableTo(t) {
		return rv, nil
	}

	if r, ok := v.(io.Reader); ok {
		data, err := io.ReadAll(r)
		if err != nil {
			return reflect.Value{}, err
		}
		v, rv = data, reflect.ValueOf(data)
		if t.Kind() == reflect.String {
			return reflect.ValueOf(string(data)).Convert(t), nil
		}
	}
	if b, ok := v.([]byte); ok && t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
		return reflect.ValueOf(b).Convert(t), nil
	}

	data, ok := v.(json.RawMessage)
	if !ok {
		var err error
		if data, err = json.Marshal(v); err != nil {
			return reflect.Value{}, err
		}
	}
	ptr := reflect.New(t)
	if err := json.Unmarshal(data, ptr.Interface()); err != nil {
		return reflect.Value{}, err
	}
	return ptr.Elem(), nil
}

// write writes a result: string, path, bytes and text raw, a stream
// copied, and anything else as JSON. A nil option writes nothing unless
// it is JSON.
func write(w io.Writer, t typ, v reflect.Value) error {
	if t.kind == "option" {
		if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil() {
			switch t.elem.kind {
			case "string", "path", "text", "bytes", "stream":
				return nil
			}
			return writeJSON(w, nil)
		}
		if v.Kind() == reflect.Pointer && t.elem.kind != "text" {
			v = v.Elem()
		}
		return write(w, *t.elem, v)
	}

	switch t.kind {
	case "string", "path":
		if v.Kind() == reflect.String {
			_, err := io.WriteString(w, v.String())
			return err
		}
		_, err := fmt.Fprint(w, v.Interface())
		return err
	case "text":
		_, err := fmt.Fprint(w, v.Interface())
		return err
	case "bytes":
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			_, err := w.Write(v.Bytes())
			return err
		}
	case "stream":
		switch x := v.Interface().(type) {
		case io.Reader:
			if c, ok := x.(io.Closer); ok {
				defer c.Close()
			}
			_, err := io.Copy(w, x)
			return err
		case []byte:
			_, err := w.Write(x)
			return err
		case string:
			_, err := io.WriteString(w, x)
			return err
		}
	}
	return writeJSON(w, v.Interface())
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
