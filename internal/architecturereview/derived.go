package architecturereview

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A request about a code-derived element names it precisely, but it is never
// part of the reviewed contract: the server re-derives the level, takes the
// element kind and breadcrumb from it, keeps the enclosing reviewed part as
// the bound target, and forbids any architecture change in the answer.
const (
	derivedMaxItems    = 50
	derivedMaxPath     = 512
	derivedMaxName     = 200
	derivedMaxPart     = 200
	derivedNote        = "derived from code, not reviewed"
	derivedMaxFileRead = codeViewMaxFileSize
)

var declarationPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)

// plainText reports a nonempty string of at most limit runes with no control
// characters.
func plainText(value string, limit int) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return false
		}
	}
	return true
}

// repoPath accepts only a clean relative slash path inside the project root.
func repoPath(value any) (string, bool) {
	p, ok := value.(string)
	if !ok || !plainText(p, derivedMaxPath) || confined(p) != p {
		return "", false
	}
	return p, true
}

type derivedItem struct{ path, to string }

type derivedAsk struct {
	part, dir, name string
	items           []derivedItem
}

// parseDerived checks the shape of the browser's derived selection before any
// lock is taken or the repository is read.
func parseDerived(raw any) (*derivedAsk, error) {
	bad := errors.New("derived code selection is invalid")
	q, ok := raw.(map[string]any)
	if !ok || !onlyKeys(q, "part", "dir", "items", "name") {
		return nil, bad
	}
	ask := &derivedAsk{}
	if ask.part, ok = q["part"].(string); !ok || !plainText(ask.part, derivedMaxPart) {
		return nil, bad
	}
	if dir, present := q["dir"]; present && dir != "" {
		if ask.dir, ok = repoPath(dir); !ok {
			return nil, errors.New("derived code path must be a clean path inside the project")
		}
	}
	items, ok := q["items"].([]any)
	if !ok || len(items) == 0 || len(items) > derivedMaxItems {
		return nil, bad
	}
	seen := map[derivedItem]bool{}
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok || !onlyKeys(item, "path", "to") {
			return nil, bad
		}
		var next derivedItem
		if next.path, ok = repoPath(item["path"]); !ok {
			return nil, errors.New("derived code path must be a clean path inside the project")
		}
		if to, present := item["to"]; present {
			if next.to, ok = repoPath(to); !ok {
				return nil, errors.New("derived code path must be a clean path inside the project")
			}
		}
		if seen[next] {
			return nil, bad
		}
		seen[next] = true
		ask.items = append(ask.items, next)
	}
	if name, present := q["name"]; present {
		text, ok := name.(string)
		if !ok || !plainText(text, derivedMaxName) || !declarationPattern.MatchString(text) {
			return nil, errors.New("declaration name is invalid")
		}
		if len(ask.items) != 1 || ask.items[0].to != "" {
			return nil, errors.New("a declaration belongs to exactly one file")
		}
		ask.name = text
	}
	return ask, nil
}

// resolveDerived re-derives the level the owner looked at and binds every
// item to an element drawn on it. It must run without r.mu held.
func (r *Review) resolveDerived(ctx context.Context, ask *derivedAsk) (map[string]any, error) {
	if r.config.CodeRoot == "" {
		return nil, errors.New("code is not available for this review")
	}
	view, err := r.CodeView(ctx, ask.part, ask.dir)
	if err != nil {
		return nil, err
	}
	if view.Layout == nil || (view.Kind != "packages" && view.Kind != "files") {
		return nil, errors.New("this code level has nothing to select")
	}
	files := map[string]string{} // node id -> its one repository path
	byPath := map[string]string{}
	for _, value := range view.Architecture["components"].([]any) {
		c := value.(map[string]any)
		list, _ := c["files"].([]string)
		if len(list) == 1 {
			files[c["id"].(string)] = list[0]
			byPath[list[0]] = c["id"].(string)
		}
	}
	edges := map[[2]string]bool{}
	for _, value := range view.Architecture["connections"].([]any) {
		c := value.(map[string]any)
		edges[[2]string{files[c["source"].(string)], files[c["target"].(string)]}] = true
	}
	root, err := os.OpenRoot(r.config.CodeRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	nodeKind := map[string]string{"packages": "package", "files": "file"}[view.Kind]
	elements := []any{}
	for _, item := range ask.items {
		for _, p := range []string{item.path, item.to} {
			if p == "" {
				continue
			}
			if _, ok := byPath[p]; !ok {
				return nil, errors.New("selected code element is not on this level")
			}
			info, err := root.Lstat(filepath.FromSlash(p))
			if err != nil || !noLinks(root, p) || !(info.Mode().IsRegular() || info.IsDir()) {
				return nil, errors.New("selected code path is not inside the project")
			}
		}
		element := map[string]any{"kind": nodeKind, "path": item.path}
		if item.to != "" {
			if !edges[[2]string{item.path, item.to}] {
				return nil, errors.New("selected code connection is not on this level")
			}
			element["kind"], element["to"] = "connection", item.to
		}
		if ask.name != "" {
			if nodeKind != "file" || !declares(root, item.path, ask.name) {
				return nil, errors.New("the file does not declare that name")
			}
			element["kind"], element["name"] = "declaration", ask.name
		}
		elements = append(elements, element)
	}
	r.mu.Lock()
	architecture, _ := r.state["architecture"].(map[string]any)
	breadcrumb := []any{}
	for cursor, guard := ask.part, 0; cursor != "" && guard < 64; guard++ {
		breadcrumb = append([]any{cursor}, breadcrumb...)
		parent := ""
		for _, value := range architecture["components"].([]any) {
			if c := value.(map[string]any); c["id"] == cursor {
				parent, _ = c["parent"].(string)
			}
		}
		cursor = parent
	}
	r.mu.Unlock()
	if view.Dir != "" {
		breadcrumb = append(breadcrumb, view.Dir)
	}
	if len(elements) == 1 {
		only := elements[0].(map[string]any)
		if only["kind"] == "file" || only["kind"] == "declaration" {
			breadcrumb = append(breadcrumb, path.Base(only["path"].(string)))
		} else if only["kind"] == "package" && only["path"] != view.Dir {
			breadcrumb = append(breadcrumb, only["path"])
		}
		if name, ok := only["name"]; ok {
			breadcrumb = append(breadcrumb, name)
		}
	}
	record := map[string]any{"part": ask.part, "elements": elements, "breadcrumb": breadcrumb, "note": derivedNote}
	if ask.dir != "" {
		record["dir"] = ask.dir
	}
	return record, nil
}

// declares reports whether a Go file declares name at package level, as a
// function, type, value, or a method written Type.Method.
func declares(root *os.Root, rel, name string) bool {
	if !strings.HasSuffix(rel, ".go") {
		return false
	}
	f, err := root.Open(filepath.FromSlash(rel))
	if err != nil {
		return false
	}
	defer f.Close()
	src, err := io.ReadAll(io.LimitReader(f, derivedMaxFileRead+1))
	if err != nil || len(src) > derivedMaxFileRead {
		return false
	}
	file, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.SkipObjectResolution)
	if err != nil {
		return false
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				if d.Name.Name == name {
					return true
				}
				continue
			}
			if len(d.Recv.List) > 0 && receiver(d.Recv.List[0].Type)+"."+d.Name.Name == name {
				return true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.Name == name {
						return true
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.Name == name {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func receiver(expr ast.Expr) string {
	for {
		switch e := expr.(type) {
		case *ast.StarExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.Ident:
			return e.Name
		default:
			return ""
		}
	}
}
