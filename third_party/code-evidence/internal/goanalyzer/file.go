package goanalyzer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Decl kinds.
const (
	KindFunc   = "func"
	KindMethod = "method"
	KindType   = "type"
	KindVar    = "var"
	KindConst  = "const"
)

// Use kinds.
const (
	UseLocal  = "local"  // bare identifier, possibly a package-level name
	UsePkg    = "pkg"    // pkg.Name through an import
	UseMember = "member" // x.Name where x is not an import (method/field)
)

// Decl is a top-level declaration with byte and line spans.
type Decl struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	Recv      string   `json:"recv,omitempty"`
	Signature string   `json:"signature"`
	Doc       string   `json:"doc,omitempty"` // first doc line
	DocStart  int      `json:"doc_start,omitempty"`
	DocLine   int      `json:"doc_line,omitempty"`
	Start     int      `json:"start"`
	End       int      `json:"end"`
	StartLine int      `json:"start_line"`
	EndLine   int      `json:"end_line"`
	Embeds    []string `json:"embeds,omitempty"`
	TestFunc  bool     `json:"test_func,omitempty"`
}

// Use is a syntactic occurrence of a (possibly) package-level name or a
// called member.
type Use struct {
	Enclosing string `json:"enclosing,omitempty"` // decl ID
	Kind      string `json:"kind"`
	Qual      string `json:"qual,omitempty"` // import path for UsePkg
	Name      string `json:"name"`
	Call      bool   `json:"call,omitempty"`
	Offset    int    `json:"offset"`
	Line      int    `json:"line"`
}

// Import is one import spec.
type Import struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path"`
	Line int    `json:"line"`
}

// Interval is a half-open byte range.
type Interval struct{ Start, End int }

// FileFacts are the tier-1 facts extracted from one Go file. They are a
// pure function of (path, bytes).
type FileFacts struct {
	Path       string     `json:"path"`
	Dir        string     `json:"dir"`
	Package    string     `json:"package"`
	IsTest     bool       `json:"is_test,omitempty"`
	Constraint string     `json:"constraint,omitempty"`
	ImportsC   bool       `json:"imports_c,omitempty"`
	ParseError string     `json:"parse_error,omitempty"`
	Imports    []Import   `json:"imports,omitempty"`
	Decls      []Decl     `json:"decls,omitempty"`
	Uses       []Use      `json:"uses,omitempty"`
	FreeText   []Interval `json:"-"` // comments and string/char literals

	constraintExpr interface{ Eval(func(string) bool) bool }
	constraintErr  bool
}

var predeclared = set("bool", "byte", "complex64", "complex128", "error", "float32", "float64",
	"int", "int8", "int16", "int32", "int64", "rune", "string", "uint", "uint8", "uint16",
	"uint32", "uint64", "uintptr", "any", "comparable", "true", "false", "iota", "nil",
	"append", "cap", "clear", "close", "complex", "copy", "delete", "imag", "len", "make",
	"max", "min", "new", "panic", "print", "println", "real", "recover", "_")

// PkgKey identifies a package within the repository: directory plus
// package name (external test packages are distinct from their package).
func PkgKey(dir, pkg string) string {
	if strings.HasSuffix(pkg, "_test") {
		return dir + "_test"
	}
	return dir
}

// DeclID builds a symbol id: "<dir>#<Name>" or "<dir>#<Recv>.<Name>"; the
// root directory is ".", external test packages use "<dir>_test".
func DeclID(dir, pkg, recv, name string) string {
	k := PkgKey(dir, pkg)
	if recv != "" {
		return k + "#" + recv + "." + name
	}
	return k + "#" + name
}

// AnalyzeFile extracts facts from src. It never panics on malformed input:
// parse failures (and recovered panics) are reported in ParseError.
func AnalyzeFile(relPath string, src []byte) (ff *FileFacts) {
	dir := path.Dir(relPath)
	ff = &FileFacts{Path: relPath, Dir: dir, IsTest: strings.HasSuffix(relPath, "_test.go")}
	defer func() {
		if r := recover(); r != nil {
			*ff = FileFacts{Path: relPath, Dir: dir, IsTest: ff.IsTest, ParseError: fmt.Sprintf("analyzer panic: %v", r)}
		}
	}()
	if x, s, err := HeaderConstraint(src); err != nil {
		ff.Constraint, ff.constraintErr = s, true
	} else if x != nil {
		ff.Constraint, ff.constraintExpr = s, x
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, relPath, src, parser.ParseComments)
	if err != nil {
		ff.ParseError = firstLine(err.Error())
		if f != nil && f.Name != nil {
			ff.Package = f.Name.Name
		}
		return ff
	}
	ff.Package = f.Name.Name
	a := &fileAnalyzer{fset: fset, src: src, ff: ff, file: f, tf: fset.File(f.Pos())}
	a.run()
	return ff
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

type fileAnalyzer struct {
	fset     *token.FileSet
	tf       *token.File
	src      []byte
	ff       *FileFacts
	file     *ast.File
	imports  map[string]string // local name -> import path
	topLevel map[any]bool      // top-level decl/spec nodes
	calls    map[ast.Node]bool // call target nodes
	skip     map[*ast.Ident]bool
}

func (a *fileAnalyzer) off(p token.Pos) int  { return a.tf.Offset(p) }
func (a *fileAnalyzer) line(p token.Pos) int { return a.tf.Line(p) }

func (a *fileAnalyzer) run() {
	ff, f := a.ff, a.file
	a.imports = map[string]string{}
	for _, is := range f.Imports {
		p, err := strconv.Unquote(is.Path.Value)
		if err != nil {
			continue
		}
		name := ""
		if is.Name != nil {
			name = is.Name.Name
		}
		ff.Imports = append(ff.Imports, Import{Name: name, Path: p, Line: a.line(is.Pos())})
		if p == "C" {
			ff.ImportsC = true
		}
		local := name
		if local == "" {
			local = importBase(p)
		}
		if local != "_" && local != "." {
			a.imports[local] = p
		}
	}
	for _, cg := range f.Comments {
		ff.FreeText = append(ff.FreeText, Interval{a.off(cg.Pos()), a.off(cg.End())})
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if bl, ok := n.(*ast.BasicLit); ok && (bl.Kind == token.STRING || bl.Kind == token.CHAR) {
			ff.FreeText = append(ff.FreeText, Interval{a.off(bl.Pos()), a.off(bl.End())})
		}
		return true
	})
	sort.Slice(ff.FreeText, func(i, j int) bool { return ff.FreeText[i].Start < ff.FreeText[j].Start })

	a.topLevel = map[any]bool{}
	for _, d := range f.Decls {
		a.topLevel[d] = true
		if gd, ok := d.(*ast.GenDecl); ok {
			for _, s := range gd.Specs {
				a.topLevel[s] = true
			}
		}
	}
	a.calls = map[ast.Node]bool{}
	a.skip = map[*ast.Ident]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			a.calls[unwrapCallee(n.Fun)] = true
		case *ast.CompositeLit:
			for _, e := range n.Elts {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					if id, ok := kv.Key.(*ast.Ident); ok {
						a.skip[id] = true // struct field key, not a reference
					}
				}
			}
		}
		return true
	})

	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			a.funcDecl(d)
		case *ast.GenDecl:
			a.genDecl(d)
		}
	}
	sort.SliceStable(ff.Decls, func(i, j int) bool { return ff.Decls[i].Start < ff.Decls[j].Start })
	sort.SliceStable(ff.Uses, func(i, j int) bool { return ff.Uses[i].Offset < ff.Uses[j].Offset })
}

func importBase(p string) string {
	b := path.Base(p)
	// Strip major-version suffixes ("v2") and common "go-" prefixes the way
	// most code names such packages; tier 1 cannot read the package clause
	// of an imported package, so this is a heuristic.
	if len(b) >= 2 && b[0] == 'v' && strings.Trim(b[1:], "0123456789") == "" {
		b = path.Base(path.Dir(p))
	}
	b = strings.TrimPrefix(b, "go-")
	b = strings.TrimSuffix(b, ".go")
	b = strings.Map(func(r rune) rune {
		if r == '-' || r == '.' {
			return '_'
		}
		return r
	}, b)
	return b
}

func unwrapCallee(e ast.Expr) ast.Node {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		default:
			return e
		}
	}
}

func recvBase(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		case *ast.Ident:
			return x.Name
		case *ast.SelectorExpr:
			return x.Sel.Name
		default:
			return ""
		}
	}
}

func typeName(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.StarExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		case *ast.Ident:
			return x.Name
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok {
				return id.Name + "." + x.Sel.Name
			}
			return x.Sel.Name
		default:
			return ""
		}
	}
}

func (a *fileAnalyzer) docOf(groups ...*ast.CommentGroup) (string, int, int) {
	for _, g := range groups {
		if g == nil {
			continue
		}
		text := strings.TrimSpace(g.Text())
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[:i]
		}
		return clip(text, 200), a.off(g.Pos()), a.line(g.Pos())
	}
	return "", 0, 0
}

// clip shortens s to at most n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}

func collapse(b []byte, n int) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	return clip(s, n)
}

func (a *fileAnalyzer) add(d Decl, pos, end token.Pos) {
	d.Start, d.End = a.off(pos), a.off(end)
	d.StartLine, d.EndLine = a.line(pos), a.line(end)
	a.ff.Decls = append(a.ff.Decls, d)
}

func isTestFunc(name string) bool {
	for _, p := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if name == p {
			return true
		}
		if rest, ok := strings.CutPrefix(name, p); ok {
			r, _ := utf8.DecodeRuneInString(rest)
			if !unicode.IsLower(r) || p == "Example" {
				return true
			}
		}
	}
	return false
}

func (a *fileAnalyzer) funcDecl(d *ast.FuncDecl) {
	ff := a.ff
	name := d.Name.Name
	recv := ""
	kind := KindFunc
	if d.Recv != nil && len(d.Recv.List) > 0 {
		recv = recvBase(d.Recv.List[0].Type)
		kind = KindMethod
	}
	id := DeclID(ff.Dir, ff.Package, recv, name)
	if name == "init" && recv == "" {
		id += "@" + strconv.Itoa(a.line(d.Pos()))
	}
	sigEnd := d.End()
	if d.Body != nil {
		sigEnd = d.Body.Lbrace
	}
	doc, docStart, docLine := a.docOf(d.Doc)
	decl := Decl{
		ID: id, Kind: kind, Name: name, Recv: recv,
		Signature: collapse(a.src[a.off(d.Pos()):a.off(sigEnd)], 240),
		Doc:       doc, DocStart: docStart, DocLine: docLine,
		TestFunc: ff.IsTest && recv == "" && isTestFunc(name),
	}
	a.add(decl, d.Pos(), d.End())
	if d.Recv != nil {
		a.walk(id, d.Recv)
	}
	a.walk(id, d.Type)
	if d.Body != nil {
		a.walk(id, d.Body)
	}
}

func (a *fileAnalyzer) genDecl(d *ast.GenDecl) {
	ff := a.ff
	single := len(d.Specs) == 1 && !d.Lparen.IsValid()
	for _, s := range d.Specs {
		switch s := s.(type) {
		case *ast.TypeSpec:
			id := DeclID(ff.Dir, ff.Package, "", s.Name.Name)
			doc, ds, dl := a.docOf(s.Doc, d.Doc)
			pos := s.Pos()
			if single {
				pos = d.Pos()
			}
			sig := "type " + s.Name.Name
			switch t := s.Type.(type) {
			case *ast.StructType:
				sig += " struct"
			case *ast.InterfaceType:
				sig += " interface"
			default:
				sig = "type " + collapse(a.src[a.off(s.Name.Pos()):a.off(t.End())], 200)
			}
			decl := Decl{ID: id, Kind: KindType, Name: s.Name.Name, Signature: sig, Doc: doc, DocStart: ds, DocLine: dl}
			if st, ok := s.Type.(*ast.StructType); ok && st.Fields != nil {
				for _, fl := range st.Fields.List {
					if len(fl.Names) == 0 {
						if n := typeName(fl.Type); n != "" {
							decl.Embeds = append(decl.Embeds, n)
						}
					}
				}
			}
			if it, ok := s.Type.(*ast.InterfaceType); ok && it.Methods != nil {
				for _, fl := range it.Methods.List {
					if len(fl.Names) == 0 {
						if n := typeName(fl.Type); n != "" {
							decl.Embeds = append(decl.Embeds, n)
						}
					}
				}
			}
			end := s.End()
			if single {
				end = d.End()
			}
			a.add(decl, pos, end)
			a.walk(id, s.TypeParams)
			a.walk(id, s.Type)
		case *ast.ValueSpec:
			kind := KindVar
			kw := "var"
			if d.Tok == token.CONST {
				kind, kw = KindConst, "const"
			}
			var first string
			for _, n := range s.Names {
				if n.Name == "_" {
					continue
				}
				id := DeclID(ff.Dir, ff.Package, "", n.Name)
				if first == "" {
					first = id
				}
				doc, ds, dl := a.docOf(s.Doc, d.Doc)
				sig := kw + " " + n.Name
				if s.Type != nil {
					sig += " " + collapse(a.src[a.off(s.Type.Pos()):a.off(s.Type.End())], 160)
				}
				pos, end := s.Pos(), s.End()
				if single {
					pos, end = d.Pos(), d.End()
				}
				a.add(Decl{ID: id, Kind: kind, Name: n.Name, Signature: sig, Doc: doc, DocStart: ds, DocLine: dl}, pos, end)
			}
			if s.Type != nil {
				a.walk(first, s.Type)
			}
			for _, v := range s.Values {
				a.walk(first, v)
			}
		}
	}
}

// walk records uses below n, attributed to the enclosing decl.
func (a *fileAnalyzer) walk(enclosing string, n ast.Node) {
	if n == nil {
		return
	}
	if fl, ok := n.(*ast.FieldList); ok && fl == nil {
		return
	}
	ast.Inspect(n, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Field:
			// Field/param names are declarations; walk only the type
			// (and tag-free) parts.
			a.walk(enclosing, x.Type)
			return false
		case *ast.LabeledStmt:
			a.walk(enclosing, x.Stmt)
			return false
		case *ast.BranchStmt:
			return false
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok && id.Obj == nil {
				if p, ok := a.imports[id.Name]; ok {
					a.use(enclosing, UsePkg, p, x.Sel, a.calls[x])
					return false
				}
			}
			if a.calls[x] {
				a.use(enclosing, UseMember, "", x.Sel, true)
			}
			a.walk(enclosing, x.X)
			return false
		case *ast.Ident:
			if a.skip[x] || predeclared[x.Name] {
				return false
			}
			if x.Obj != nil {
				if !a.topLevel[x.Obj.Decl] {
					return false // local variable, parameter, label, ...
				}
			}
			a.use(enclosing, UseLocal, "", x, a.calls[x])
			return false
		}
		return true
	})
}

func (a *fileAnalyzer) use(enclosing, kind, qual string, id *ast.Ident, call bool) {
	a.ff.Uses = append(a.ff.Uses, Use{
		Enclosing: enclosing, Kind: kind, Qual: qual, Name: id.Name, Call: call,
		Offset: a.off(id.Pos()), Line: a.line(id.Pos()),
	})
}

// InFreeText reports whether byte offset off lies inside a comment or a
// string/char literal.
func (ff *FileFacts) InFreeText(off int) bool {
	iv := ff.FreeText
	i := sort.Search(len(iv), func(i int) bool { return iv[i].End > off })
	return i < len(iv) && iv[i].Start <= off
}
