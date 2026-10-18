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

// primitives maps TDL's standard prelude primitives onto the neutral set.
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

// Module is the package of the lib2cli module, lib/lib2cli.tdl.
const Module = "lib2cli"

// marks are the lib2cli primitives, keyed by their name in the module, with the
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

// reserved are the flags every generated command defines for itself.
var reserved = map[string]bool{"json": true, "help": true}

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

	// objects records whether each struct a command uses maps, so its
	// fields are checked and reported once. A struct still being checked
	// counts as mapped, which lets a recursive type terminate.
	objects map[string]bool
}

// errorf reports an error at pos, which may be nil for the whole model.
func (b *builder) errorf(pos *ir.Position, format string, args ...any) {
	b.diags = append(b.diags, &plugin.Diagnostic{
		Severity: plugin.Severity_SEVERITY_ERROR,
		Message:  fmt.Sprintf(format, args...),
		Position: pos,
	})
}

// plan builds the whole plan: the binding, then a command per Fn field of
// each type tagged cli.
func (b *builder) plan() *Plan {
	p := &Plan{Commands: []Command{}}
	b.bind(p)

	found := false
	seen := map[string]string{} // subcommand -> the field it came from
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
			sub := kebab(f.GetMeta().GetName())
			name := d.GetMeta().GetName() + "." + f.GetMeta().GetName()
			if prev, dup := seen[sub]; dup {
				b.errorf(f.GetMeta().GetPosition(), "%s and %s are both the subcommand %s", prev, name, sub)
				continue
			}
			seen[sub] = name
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
		b.errorf(pos, "%s is not a function: its type must be Fn<Request, Result>, from the lib2cli module", name)
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
	flags := map[string]string{} // flag -> the field it came from
	for _, rf := range reqDecl.Fields() {
		fname, fpos := rf.GetMeta().GetName(), rf.GetMeta().GetPosition()
		flag := kebab(fname)
		if reserved[flag] {
			b.errorf(fpos, "%s is the flag --%s, which every command reserves", fname, flag)
			ok = false
		} else if prev, dup := flags[flag]; dup {
			b.errorf(fpos, "%s and %s are both the flag --%s", prev, fname, flag)
			ok = false
		}
		flags[flag] = fname

		pt, good := b.resolve(rf.GetType(), fpos)
		ok = ok && good
		c.Params = append(c.Params, Param{Name: fname, Flag: flag, Type: pt})
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
		if !ok {
			return Type{}, false
		}
		if key.Kind != KindString {
			b.errorf(pos, "%s has no CLI mapping: a map's key must be a string", id.GetName())
			return Type{}, false
		}
		return b.wrap(KindMap, args[1], pos)
	case decl.GetStructure() != nil && len(args) == 0:
		return b.object(decl)
	case decl.GetNewtype() != nil && len(args) == 0:
		return b.resolve(decl.GetNewtype().GetBase(), pos)
	case decl.GetAlias() != nil && len(args) == 0:
		return b.resolve(decl.GetAlias().GetTarget(), pos)
	}

	b.errorf(pos, "%s has no CLI mapping", id.GetName())
	return Type{}, false
}

// object maps a struct decoded from JSON, checking that each of its fields
// maps too.
func (b *builder) object(decl *ir.Decl) (Type, bool) {
	name := decl.GetMeta().GetName()
	t := Type{Kind: KindObject, Name: &name}
	if ok, seen := b.objects[name]; seen {
		return t, ok
	}
	if b.objects == nil {
		b.objects = map[string]bool{}
	}
	b.objects[name] = true

	ok := true
	for _, f := range decl.Fields() {
		_, good := b.resolve(f.GetType(), f.GetMeta().GetPosition())
		ok = ok && good
	}
	b.objects[name] = ok
	return t, ok
}

// mark says which lib2cli primitive a type is, if any: one imported from
// the lib2cli module, or a model's own primitive tagged in the target block.
func (b *builder) mark(t *ir.Type) string {
	if id := t.GetExtern(); id != nil {
		externs := b.model.GetExterns()
		if i := int(id.GetIndex()); i < len(externs) && externs[i].GetPackage() == Module {
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

// wrap resolves elem and wraps it in a list, map or option.
func (b *builder) wrap(kind Kind, elem *ir.ID, pos *ir.Position) (Type, bool) {
	e, ok := b.resolve(elem, pos)
	return Type{Kind: kind, Elem: &e}, ok
}

// tagged says whether dirs hold this target's directive name.
func (b *builder) tagged(dirs []*ir.Directive, name string) bool {
	for _, d := range plugin.Directives(b.target, dirs) {
		if d.GetName() == name {
			return true
		}
	}
	return false
}

// arg is a directive's i-th argument as text, or "" if it has none.
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
