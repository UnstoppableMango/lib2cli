package backend

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"

	"github.com/unstoppablemango/tdl/ir"
	"github.com/unstoppablemango/tdl/plugin"
)

// Plan is what lib2cli will generate for one model: a binary bound to one
// library, with a subcommand per function.
type Plan struct {
	// Lang is the target language, "go" or "ts", picked by the binding
	// directive.
	Lang string `json:"lang"`

	// Package is the Go module or npm package the functions live in, and
	// Version the exact version the generated project pins.
	Package string `json:"package"`
	Version string `json:"version"`

	// Bin is the generated binary's name.
	Bin string `json:"bin"`

	Commands []Command `json:"commands"`
}

// Command is one function, called as a subcommand.
type Command struct {
	// Name is the subcommand, the field's name in kebab case.
	Name string `json:"name"`

	// Func is the library function the command calls.
	Func string `json:"func"`

	// Whole says the function takes the request as one argument rather
	// than its fields as positional parameters.
	Whole bool `json:"whole,omitempty"`

	// Params are the request's fields, in order. Each is a flag.
	Params []Param `json:"params"`

	Result Type `json:"result"`
}

// Param is one field of a command's request.
type Param struct {
	Name string `json:"name"`
	Flag string `json:"flag"`
	Type Type   `json:"type"`
}

// Kind is a type in lib2cli's language-neutral set.
type Kind string

const (
	KindString Kind = "string"
	KindInt    Kind = "int"
	KindFloat  Kind = "float"
	KindBool   Kind = "bool"
	KindBytes  Kind = "bytes"
	KindPath   Kind = "path"
	KindStream Kind = "stream"
	KindText   Kind = "text"
	KindList   Kind = "list"
	KindMap    Kind = "map"
	KindObject Kind = "object"
	KindOption Kind = "option"
)

// Type is a parameter or result type.
type Type struct {
	Kind Kind `json:"kind"`

	// Name is the declaration an object decodes into.
	Name string `json:"name,omitempty"`

	// Elem is the element of a list or map, or the value of an option.
	Elem *Type `json:"elem,omitempty"`
}

// primitives maps the prelude's primitives onto the neutral set.
var primitives = map[string]Kind{
	"string":  KindString,
	"int":     KindInt,
	"int32":   KindInt,
	"int64":   KindInt,
	"uint32":  KindInt,
	"uint64":  KindInt,
	"float32": KindFloat,
	"float64": KindFloat,
	"bool":    KindBool,
	"bytes":   KindBytes,
}

// Prelude is the package of the lib2cli prelude, prelude/lib2cli.tdl.
const Prelude = "lib2cli"

// marks are the lib2cli primitives, keyed by their prelude name, with the
// directive that marks a model's own declaration as the same thing.
var marks = map[string]string{
	"Fn":     dirFn,
	"Text":   dirText,
	"Path":   dirPath,
	"Stream": dirStream,
}

// markKinds are the primitives whose values have a lib2cli meaning.
var markKinds = map[string]Kind{
	dirText:   KindText,
	dirPath:   KindPath,
	dirStream: KindStream,
}

// Build plans the shim for a request, reporting every problem it finds.
func Build(req *plugin.Request) (*Plan, []*plugin.Diagnostic) {
	target := req.GetTarget()
	if target == "" {
		target = Target
	}
	b := &builder{model: req.GetModel(), target: target}
	return b.plan(), b.diags
}

type builder struct {
	model  *ir.Model
	target string
	diags  []*plugin.Diagnostic
}

func (b *builder) errorf(pos *ir.Position, format string, args ...any) {
	b.diags = append(b.diags, &plugin.Diagnostic{
		Severity: plugin.Severity_SEVERITY_ERROR,
		Message:  fmt.Sprintf(format, args...),
		Position: pos,
	})
}

func (b *builder) plan() *Plan {
	p := &Plan{Commands: []Command{}}
	b.bind(p)

	found := false
	for _, d := range b.model.GetDecls() {
		if !b.tagged(d.GetDirectives(), dirCli) {
			continue
		}
		found = true
		if d.GetStructure() == nil {
			b.errorf(d.GetMeta().GetPosition(), "%s is tagged cli but is not a struct", d.GetMeta().GetName())
			continue
		}
		for _, f := range d.Fields() {
			if c, ok := b.command(f); ok {
				p.Commands = append(p.Commands, c)
			}
		}
	}
	if !found {
		b.errorf(nil, "no type is tagged cli: tag the type holding the functions, as in `Lib => cli`")
	}
	return p
}

// bind reads the target block's binding and binary name.
func (b *builder) bind(p *Plan) {
	var block *ir.TargetBlock
	for _, t := range b.model.GetTargets() {
		if t.GetMeta().GetName() == b.target {
			block = t
		}
	}

	var bindings []*ir.Directive
	for _, d := range plugin.Directives(b.target, block.GetDirectives()) {
		switch d.GetName() {
		case dirGo, dirNpm:
			bindings = append(bindings, d)
		case dirBin:
			p.Bin = arg(d, 0)
		}
	}

	switch len(bindings) {
	case 0:
		b.errorf(block.GetMeta().GetPosition(), "the target block names no library: add go(module, version) or npm(package, version)")
		return
	case 1:
	default:
		b.errorf(bindings[1].GetPosition(), "the target block names more than one library; a file describes one")
		return
	}

	d := bindings[0]
	p.Package, p.Version = arg(d, 0), arg(d, 1)
	if d.GetName() == dirGo {
		p.Lang = "go"
	} else {
		p.Lang = "ts"
	}
	if p.Bin == "" {
		p.Bin = defaultBin(p.Package)
	}
}

var majorSuffix = regexp.MustCompile(`^v[0-9]+$`)

// defaultBin names the binary after the package's last path segment,
// skipping a Go major version suffix.
func defaultBin(pkg string) string {
	pkg = strings.TrimSuffix(pkg, "/")
	base := path.Base(pkg)
	if majorSuffix.MatchString(base) {
		base = path.Base(path.Dir(pkg))
	}
	return base
}

// command plans one field of a cli type.
func (b *builder) command(f *ir.Field) (Command, bool) {
	name := f.GetMeta().GetName()
	pos := f.GetMeta().GetPosition()

	t := b.model.Type(f.GetType())
	if b.mark(t) != dirFn || len(t.GetArgs()) != 2 {
		b.errorf(pos, "%s is not a function: its type must be Fn<Request, Result>, from the lib2cli prelude", name)
		return Command{}, false
	}

	c := Command{Name: kebab(name), Func: name, Params: []Param{}}
	for _, d := range plugin.Directives(b.target, f.GetDirectives()) {
		switch d.GetName() {
		case dirFunc:
			c.Func = arg(d, 0)
		case dirWhole:
			c.Whole = true
		}
	}

	reqType := b.model.Type(t.GetArgs()[0])
	reqDecl := b.model.Decl(reqType.GetCtor())
	if reqType.GetCtor() == nil || reqDecl.GetStructure() == nil || len(reqType.GetArgs()) != 0 {
		b.errorf(pos, "the request of %s must be a struct", name)
		return Command{}, false
	}

	ok := true
	for _, rf := range reqDecl.Fields() {
		pt, good := b.resolve(rf.GetType(), rf.GetMeta().GetPosition())
		ok = ok && good
		c.Params = append(c.Params, Param{
			Name: rf.GetMeta().GetName(),
			Flag: kebab(rf.GetMeta().GetName()),
			Type: pt,
		})
	}

	result, good := b.resolve(t.GetArgs()[1], pos)
	c.Result = result
	return c, ok && good
}

// resolve maps a type onto the neutral set, reporting a type with no
// mapping at pos.
func (b *builder) resolve(id *ir.ID, pos *ir.Position) (Type, bool) {
	t := b.model.Type(id)
	if t == nil || t.GetParam() != nil {
		b.errorf(pos, "%s has no CLI mapping", id.GetName())
		return Type{}, false
	}

	if kind, ok := markKinds[b.mark(t)]; ok && len(t.GetArgs()) == 0 {
		return Type{Kind: kind}, true
	}
	if t.GetCtor() == nil {
		b.errorf(pos, "%s has no CLI mapping", id.GetName())
		return Type{}, false
	}

	decl := b.model.Decl(t.GetCtor())
	name := decl.GetMeta().GetName()
	args := t.GetArgs()

	switch {
	case decl.GetPrimitive() != nil && len(args) == 0:
		if kind, ok := primitives[name]; ok {
			return Type{Kind: kind}, true
		}
	case name == "List" && len(args) == 1, name == "Set" && len(args) == 1:
		return b.wrap(KindList, args[0], pos)
	case name == "Option" && len(args) == 1, name == "Nullable" && len(args) == 1:
		return b.wrap(KindOption, args[0], pos)
	case name == "Map" && len(args) == 2:
		key, ok := b.resolve(args[0], pos)
		if ok && key.Kind != KindString {
			b.errorf(pos, "%s has no CLI mapping: a map's key must be a string", id.GetName())
			return Type{}, false
		}
		return b.wrap(KindMap, args[1], pos)
	case decl.GetStructure() != nil && len(args) == 0:
		return Type{Kind: KindObject, Name: name}, true
	case decl.GetNewtype() != nil && len(args) == 0:
		return b.resolve(decl.GetNewtype().GetBase(), pos)
	case decl.GetAlias() != nil && len(args) == 0:
		return b.resolve(decl.GetAlias().GetTarget(), pos)
	}

	b.errorf(pos, "%s has no CLI mapping", id.GetName())
	return Type{}, false
}

// mark says which lib2cli primitive a type is, if any: one imported from
// the prelude, or a model's own primitive tagged in the target block.
func (b *builder) mark(t *ir.Type) string {
	if id := t.GetExtern(); id != nil {
		externs := b.model.GetExterns()
		if i := int(id.GetIndex()); i < len(externs) && externs[i].GetPackage() == Prelude {
			return marks[externs[i].GetName()]
		}
		return ""
	}
	if t.GetCtor() == nil {
		return ""
	}
	decl := b.model.Decl(t.GetCtor())
	for _, m := range marks {
		if decl.GetPrimitive() != nil && b.tagged(decl.GetDirectives(), m) {
			return m
		}
	}
	return ""
}

func (b *builder) wrap(kind Kind, elem *ir.ID, pos *ir.Position) (Type, bool) {
	e, ok := b.resolve(elem, pos)
	return Type{Kind: kind, Elem: &e}, ok
}

func (b *builder) tagged(dirs []*ir.Directive, name string) bool {
	for _, d := range plugin.Directives(b.target, dirs) {
		if d.GetName() == name {
			return true
		}
	}
	return false
}

func arg(d *ir.Directive, i int) string {
	if args := d.GetArgs(); i < len(args) {
		return args[i].GetText()
	}
	return ""
}

// kebab turns a camelCase or snake_case name into a flag or subcommand.
func kebab(name string) string {
	var sb strings.Builder
	runes := []rune(name)
	for i, r := range runes {
		switch {
		case r == '_':
			sb.WriteByte('-')
		case unicode.IsUpper(r):
			if i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])) {
				sb.WriteByte('-')
			}
			sb.WriteRune(unicode.ToLower(r))
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
