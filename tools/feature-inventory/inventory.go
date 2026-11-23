// Package inventory validates the version 1 feature inventory without executing
// application code. Source owners can declare
// coverage keys in comments as "feature-inventory: ID"; every such key must have
// a feature or pending row.
package inventory

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

type Inventory struct {
	Version  int       `yaml:"version"`
	Features []Feature `yaml:"features"`
	Pending  []Pending `yaml:"pending"`
	Later    []Later   `yaml:"later"`
}
type Feature struct {
	ID       string    `yaml:"id"`
	Name     string    `yaml:"name"`
	Editions []string  `yaml:"editions"`
	Sources  []string  `yaml:"sources"`
	Tests    []string  `yaml:"tests"`
	Docs     []string  `yaml:"docs"`
	Web      []Surface `yaml:"web"`
	TUI      []Surface `yaml:"tui"`
}
type Surface struct {
	RouteOrCommand string `yaml:"routeOrCommand"`
	Anchor         string `yaml:"anchor"`
	Kind           string `yaml:"kind"`
	Reason         string `yaml:"reason,omitempty"`
}
type Pending struct {
	ID                   string `yaml:"id"`
	Missing              string `yaml:"missing"`
	Source               string `yaml:"source"`
	ExistingStub         bool   `yaml:"existingStub,omitempty"`
	PendingScopeDecision bool   `yaml:"pendingScopeDecision,omitempty"`
}
type Later struct {
	ID       string `yaml:"id"`
	Decision string `yaml:"decision"`
}

func Load(r io.Reader) (Inventory, error) {
	var inv Inventory
	data, err := io.ReadAll(r)
	if err != nil {
		return inv, err
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&inv); err != nil {
		return inv, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return inv, fmt.Errorf("inventory must contain one YAML document")
	}
	// Typed decoding alone cannot distinguish a missing list from an
	// explicitly empty list.
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return inv, err
	}
	if len(document.Content) != 1 {
		return inv, fmt.Errorf("inventory must be a mapping")
	}
	root := document.Content[0]
	if err := requireFields(root, "inventory", "version", "features", "pending", "later"); err != nil {
		return inv, err
	}
	for n := 0; n < len(root.Content); n += 2 {
		key, value := root.Content[n].Value, root.Content[n+1]
		if key == "version" {
			continue
		}
		if value.Kind != yaml.SequenceNode {
			return inv, fmt.Errorf("%s must be a list", key)
		}
		if key != "features" {
			continue
		}
		for _, row := range value.Content {
			if err := requireFields(row, "feature", "id", "name", "editions", "sources", "tests", "docs", "web", "tui"); err != nil {
				return inv, err
			}
			for j := 0; j < len(row.Content); j += 2 {
				if slices.Contains([]string{"editions", "sources", "tests", "docs", "web", "tui"}, row.Content[j].Value) && row.Content[j+1].Kind != yaml.SequenceNode {
					return inv, fmt.Errorf("feature %s must be a list", row.Content[j].Value)
				}
			}
		}
	}
	return inv, nil
}

func requireFields(node *yaml.Node, context string, fields ...string) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s must be a mapping", context)
	}
	for _, field := range fields {
		found := false
		for i := 0; i < len(node.Content); i += 2 {
			if node.Content[i].Value == field {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s requires %s", context, field)
		}
	}
	return nil
}

// Validate accepts an immutable pending baseline separately from the YAML. Exact
// rows may be deleted, but changing an exception's scope requires baseline review.
// All failures are collected so CI reports every affected coverage ID.
func Validate(repo fs.FS, inv Inventory, frozen []Pending) error {
	var problems []error
	fail := func(id, message string) { problems = append(problems, fmt.Errorf("%s: %s", id, message)) }
	if inv.Version != 1 {
		fail("inventory", "version must be 1")
	}
	if len(inv.Features) == 0 {
		fail("inventory", "features must not be empty")
	}
	pending := map[string]Pending{}
	for _, p := range inv.Pending {
		if p.ID == "" || p.Missing == "" || p.Source == "" {
			fail(p.ID, "pending requires id, missing and source")
		}
		if p.Missing != "web" && p.Missing != "tui" && p.Missing != "both" {
			fail(p.ID, "pending missing must be web, tui or both")
		}
		if _, exists := pending[p.ID]; exists {
			fail(p.ID, "duplicate pending ID")
		}
		pending[p.ID] = p
		if !slices.Contains(frozen, p) {
			fail(p.ID, "new or expanded pending exception")
		}
	}
	ids := map[string]bool{}
	for _, f := range inv.Features {
		if f.ID == "" || f.Name == "" || len(f.Sources) == 0 || len(f.Editions) == 0 {
			fail(f.ID, "feature requires id, name, sources and editions")
		}
		if ids[f.ID] {
			fail(f.ID, "duplicate feature ID")
		}
		ids[f.ID] = true
		seenEditions := map[string]bool{}
		for _, edition := range f.Editions {
			if (edition != "default" && edition != "governed") || seenEditions[edition] {
				fail(f.ID, "invalid or duplicate edition: "+edition)
			}
			seenEditions[edition] = true
		}
		for _, source := range f.Sources {
			file, symbol, _ := strings.Cut(source, ":")
			data, err := readRepo(repo, file)
			if err != nil {
				fail(f.ID, "source "+source+": "+err.Error())
				continue
			}
			if symbol != "" && !hasSymbol(file, data, symbol) {
				fail(f.ID, "source symbol not found: "+source)
			}
		}
		for _, doc := range f.Docs {
			if _, err := readRepo(repo, doc); err != nil {
				fail(f.ID, "document "+doc+": "+err.Error())
			}
		}
		for _, test := range f.Tests {
			if !testExists(repo, test) {
				fail(f.ID, "test not found: "+test)
			}
		}
		p := pending[f.ID]
		for _, surface := range []struct {
			name string
			rows []Surface
			tui  bool
		}{
			{"web", f.Web, false},
			{"tui", f.TUI, true},
		} {
			missing := p.Missing == surface.name || p.Missing == "both"
			if missing && len(surface.rows) != 0 {
				fail(f.ID, "stale pending: "+surface.name+" is listed as missing but has surfaces")
			}
			for _, edition := range f.Editions {
				if !missing && len(surface.rows) == 0 {
					fail(f.ID, edition+" "+surface.name+": no reachable surface; pending must name absent surfaces")
				}
				for _, row := range surface.rows {
					if err := checkSurface(repo, f, row, surface.tui, edition); err != nil {
						fail(f.ID, edition+" "+surface.name+": "+err.Error())
					}
				}
			}
		}
	}
	laterIDs := map[string]bool{}
	for _, l := range inv.Later {
		if l.ID == "" || strings.TrimSpace(l.Decision) == "" || laterIDs[l.ID] {
			fail(l.ID, "later requires a unique id and an approval decision")
		}
		laterIDs[l.ID] = true
	}
	// Explicit source declarations are independent of the YAML, so deleting a row
	// cannot silently remove coverage for a declared feature.
	err := fs.WalkDir(repo, ".", func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if file != "." && (strings.HasPrefix(entry.Name(), ".") || file == "third_party" || file == "tools" || entry.Name() == "testdata") {
				return fs.SkipDir
			}
			return nil
		}
		ext := path.Ext(file)
		if ext != ".go" && ext != ".js" && ext != ".html" && ext != ".md" {
			return nil
		}
		if strings.HasSuffix(file, "_test.go") {
			return nil
		}
		data, err := fs.ReadFile(repo, file)
		if err != nil {
			return err
		}
		if ext != ".md" {
			for _, match := range sourceMarker.FindAllStringSubmatch(string(data), -1) {
				if !ids[match[1]] {
					if _, ok := pending[match[1]]; !ok {
						fail(match[1], "orphan source key in "+file)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

var sourceMarker = regexp.MustCompile(`(?:^|\n)\s*(?://|/\*|\*|<!--)\s*feature-inventory:\s*([A-Za-z0-9][A-Za-z0-9_-]*)`)

func readRepo(repo fs.FS, file string) ([]byte, error) {
	if !fs.ValidPath(file) || file == "." || strings.Contains(file, `\`) {
		return nil, fmt.Errorf("expected repo-relative file path")
	}
	return fs.ReadFile(repo, file)
}

func hasSymbol(file string, data []byte, symbol string) bool {
	node, err := parser.ParseFile(token.NewFileSet(), file, data, 0)
	if err != nil {
		return false
	}
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			if n.Name.Name == symbol {
				found = true
			}
			if n.Recv != nil {
				for _, r := range n.Recv.List {
					typ := r.Type
					if p, ok := typ.(*ast.StarExpr); ok {
						typ = p.X
					}
					if id, ok := typ.(*ast.Ident); ok && id.Name+"."+n.Name.Name == strings.TrimPrefix(symbol, "*") {
						found = true
					}
				}
			}
		case *ast.TypeSpec:
			if n.Name.Name == symbol {
				found = true
			}
		case *ast.ValueSpec:
			for _, name := range n.Names {
				if name.Name == symbol {
					found = true
				}
			}
		}
		return true
	})
	return found
}

func testExists(repo fs.FS, ref string) bool {
	pkg, name, ok := strings.Cut(ref, ".Test")
	if !ok || pkg == "" || name == "" {
		return false
	}
	name = "Test" + name
	if module, err := fs.ReadFile(repo, "go.mod"); err == nil {
		for _, line := range strings.Split(string(module), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "module" {
				pkg = strings.TrimPrefix(pkg, fields[1]+"/")
				break
			}
		}
	}
	// Full repo-relative package paths are preferred; bare package names are
	// accepted only when the declaration actually belongs to that package.
	found := false
	_ = fs.WalkDir(repo, ".", func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || file == "third_party") {
			return fs.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(file, "_test.go") {
			return nil
		}
		if path.Dir(file) != pkg && (strings.Contains(pkg, "/") || path.Base(path.Dir(file)) != pkg) {
			return nil
		}
		data, err := fs.ReadFile(repo, file)
		if err != nil {
			return err
		}
		node, err := parser.ParseFile(token.NewFileSet(), file, data, 0)
		if err != nil {
			return err
		}
		for _, decl := range node.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == name && fn.Type.Params.NumFields() == 1 {
				found = true
			}
		}
		return nil
	})
	return found
}

func checkSurface(repo fs.FS, f Feature, s Surface, tui bool, edition string) error {
	if strings.TrimSpace(s.Anchor) == "" || strings.TrimSpace(s.RouteOrCommand) == "" {
		return fmt.Errorf("surface requires routeOrCommand and anchor")
	}
	if s.Kind != "view" && s.Kind != "control" && s.Kind != "locked" {
		return fmt.Errorf("invalid surface kind %q", s.Kind)
	}
	if s.Kind == "locked" && strings.TrimSpace(s.Reason) == "" {
		return fmt.Errorf("locked surface requires a reason")
	}
	if !tui {
		anchor := strings.TrimSpace(s.Anchor)
		// Require a useful literal, while allowing simple function declarations.
		words := strings.FieldsFunc(anchor, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		specific := false
		for _, word := range words {
			if !slices.Contains(strings.Fields("function return var let const if else for while switch case break continue default true false null undefined new class extends super static async await yield try catch finally throw typeof void delete instanceof in of do this with debugger export import enum implements interface package private protected public"), word) {
				specific = true
			}
		}
		if utf8.RuneCountInString(anchor) < 12 || !specific {
			return fmt.Errorf("web anchor must be at least 12 characters and contain more than punctuation or keywords")
		}
	}
	file := "internal/webboard/page/index.html"
	if tui {
		file = ""
		for _, source := range f.Sources {
			candidate, _, _ := strings.Cut(source, ":")
			if strings.HasPrefix(candidate, "internal/tui/") {
				file = candidate
				break
			}
		}
		if file == "" {
			return fmt.Errorf("TUI surface has no internal/tui source")
		}
	}
	data, err := readRepo(repo, file)
	if err != nil {
		return err
	}
	text := string(data)
	if !tui {
		// The Web page is index.html plus the app.js that renders it; an
		// anchor or locked reason may live in either.
		script, err := readRepo(repo, "internal/webboard/page/app.js")
		if err != nil {
			return err
		}
		text += "\n" + string(script)
		file = "internal/webboard/page/{index.html,app.js}"
	}
	if !strings.Contains(text, s.Anchor) {
		return fmt.Errorf("anchor %q not found in %s", s.Anchor, file)
	}
	if s.Kind == "locked" && !strings.Contains(text, s.Reason) {
		return fmt.Errorf("locked reason is not rendered in %s", file)
	}
	if tui {
		// routeOrCommand names how the user reaches it; the anchor is the
		// literal that proves the source handles it.
		if strings.HasSuffix(file, ".go") {
			ctx := build.Default
			ctx.GOOS = "linux"
			ctx.GOARCH = "amd64"
			ctx.BuildTags = nil
			ctx.OpenFile = func(name string) (io.ReadCloser, error) {
				b, err := readRepo(repo, name)
				if err != nil {
					return nil, err
				}
				return io.NopCloser(strings.NewReader(string(b))), nil
			}
			if edition == "governed" {
				ctx.BuildTags = []string{"governed"}
			}
			match, err := ctx.MatchFile(path.Dir(file), path.Base(file))
			if err != nil || !match {
				return fmt.Errorf("source %s is unavailable in %s", file, edition)
			}
		}
	} else {
		if strings.Contains(strings.ToLower(text), "coming soon") {
			return fmt.Errorf("web page %s contains Coming soon", file)
		}
		route := strings.TrimPrefix(s.RouteOrCommand, "#")
		if !strings.HasPrefix(s.RouteOrCommand, "#") || !hasHTMLAttribute(text, "href", "#"+route) || !hasHTMLAttribute(text, "id", "s-"+route) {
			return fmt.Errorf("web route %q lacks navigation or target", s.RouteOrCommand)
		}
		script, err := readRepo(repo, "internal/webboard/page/app.js")
		if err != nil {
			return err
		}
		sections := regexp.MustCompile(`(?:var|let|const)\s+sections\s*=\s*\[([^\]]*)\]`).FindSubmatch(script)
		if len(sections) != 2 || (!strings.Contains(string(sections[1]), `"`+route+`"`) && !strings.Contains(string(sections[1]), `'`+route+`'`)) {
			return fmt.Errorf("web route %q is absent from the router", s.RouteOrCommand)
		}
	}
	return nil
}

func hasHTMLAttribute(text, name, value string) bool {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\s*=\s*["']` + regexp.QuoteMeta(value) + `["']`).MatchString(text)
}
