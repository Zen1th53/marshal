package architecturereview

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The code view is a read-only level the browser derives from the repository
// for a part that declares no sub-parts. It is labelled "derived from code,
// not reviewed", is never stored in the review state and never takes part in
// what the owner accepts. Every bound below keeps it cheap and confined.
const (
	codeViewMaxPackages = 40
	codeViewMaxFiles    = 60
	codeViewMaxFileSize = 512 << 10
	codeViewMaxDepth    = 6
	codeViewTimeLimit   = 2 * time.Second
	codeViewMaxWalked   = 4000
)

// CodeView is the derived level the page draws: an architecture in the
// review's own shape plus its generic drawing.
type CodeView struct {
	Derived      bool              `json:"derived"`
	Part         string            `json:"part"`
	Dir          string            `json:"dir,omitempty"`
	Kind         string            `json:"kind"` // "packages", "files" or "empty"
	Truncated    bool              `json:"truncated,omitempty"`
	Architecture map[string]any    `json:"architecture"`
	Layout       *Layout           `json:"layout,omitempty"`
	Drill        map[string]string `json:"drill,omitempty"` // node id -> dir that opens one level deeper
}

type codeNode struct {
	id, name, does string
	files          []string
	dir            string // a package that can be opened
}

type codeEdge struct{ source, target, label string }

// confined turns a contract path into a clean slash path inside the root, or
// "" when it escapes the root, is absolute or names a hidden element.
func confined(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || strings.ContainsRune(p, 0) || filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "*?[") {
		return ""
	}
	clean := path.Clean(filepath.ToSlash(p))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return ""
	}
	for _, element := range strings.Split(clean, "/") {
		if strings.HasPrefix(element, ".") {
			return ""
		}
	}
	return clean
}

func skippedDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || name == "node_modules"
}

// regular reports a regular file below root without following links.
func regular(root, rel string) (os.FileInfo, bool) {
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return info, err == nil && info.Mode().IsRegular()
}

// noLinks reports that no element of rel below root is a symbolic link.
func noLinks(root, rel string) bool {
	current := root
	for _, element := range strings.Split(rel, "/") {
		current = filepath.Join(current, element)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}

type codeScan struct {
	root     string
	deadline time.Time
	walked   int
	cut      bool
}

func (s *codeScan) expired() bool { return time.Now().After(s.deadline) }

// packages lists the directories holding Go files under the part's paths, and
// the explicit non-directory files the paths name.
func (s *codeScan) packages(paths []string) (dirs []string, files []string) {
	seenDir, seenFile := map[string]bool{}, map[string]bool{}
	for _, raw := range paths {
		rel := confined(raw)
		if rel == "" || !noLinks(s.root, rel) {
			continue
		}
		info, err := os.Lstat(filepath.Join(s.root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		if info.Mode().IsRegular() {
			if !seenFile[rel] {
				seenFile[rel] = true
				files = append(files, rel)
			}
			continue
		}
		if !info.IsDir() {
			continue
		}
		base := filepath.Join(s.root, filepath.FromSlash(rel))
		_ = filepath.WalkDir(base, func(full string, d fs.DirEntry, err error) error {
			if err != nil || s.expired() || s.walked >= codeViewMaxWalked {
				s.cut = s.cut || err == nil
				return fs.SkipAll
			}
			s.walked++
			sub, _ := filepath.Rel(s.root, full)
			sub = filepath.ToSlash(sub)
			if d.IsDir() {
				if full != base && (skippedDir(d.Name()) || strings.Count(strings.TrimPrefix(sub, rel), "/") > codeViewMaxDepth) {
					return fs.SkipDir
				}
				return nil
			}
			if d.Type().IsRegular() && strings.HasSuffix(d.Name(), ".go") && !strings.HasSuffix(d.Name(), "_test.go") {
				dir := path.Dir(sub)
				if !seenDir[dir] {
					if len(dirs) >= codeViewMaxPackages {
						s.cut = true
						return fs.SkipAll
					}
					seenDir[dir] = true
					dirs = append(dirs, dir)
				}
			}
			return nil
		})
	}
	sort.Strings(dirs)
	sort.Strings(files)
	return dirs, files
}

// goFiles lists a directory's non-test Go files (or, when it has none, its
// regular files), bounded.
func (s *codeScan) goFiles(dir string) (names []string, gocode bool) {
	entries, err := os.ReadDir(filepath.Join(s.root, filepath.FromSlash(dir)))
	if err != nil {
		return nil, false
	}
	var others []string
	for _, e := range entries {
		if !e.Type().IsRegular() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			names = append(names, e.Name())
		} else {
			others = append(others, e.Name())
		}
	}
	if len(names) == 0 {
		names, gocode = others, false
	} else {
		gocode = true
	}
	if len(names) > codeViewMaxFiles {
		names, s.cut = names[:codeViewMaxFiles], true
	}
	return names, gocode
}

func modulePath(root string) string {
	info, ok := regular(root, "go.mod")
	if !ok || info.Size() > codeViewMaxFileSize {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "module" {
			return strings.Trim(fields[1], `"`)
		}
	}
	return ""
}

func (s *codeScan) parse(rel string, mode parser.Mode) *ast.File {
	if s.expired() {
		s.cut = true
		return nil
	}
	info, ok := regular(s.root, rel)
	if !ok || info.Size() > codeViewMaxFileSize {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	file, _ := parser.ParseFile(token.NewFileSet(), rel, data, mode|parser.SkipObjectResolution)
	return file
}

// packageLevel: one node per package, an edge per import between them.
func (s *codeScan) packageLevel(dirs []string) ([]codeNode, []codeEdge) {
	module := modulePath(s.root)
	common := dirs[0]
	for _, d := range dirs[1:] {
		for common != "." && d != common && !strings.HasPrefix(d, common+"/") {
			common = path.Dir(common)
		}
	}
	index := map[string]string{}
	nodes := []codeNode{}
	for i, dir := range dirs {
		id := "pkg-" + strconv.Itoa(i+1)
		index[dir] = id
		name := strings.TrimPrefix(strings.TrimPrefix(dir, common), "/")
		if name == "" {
			name = path.Base(dir)
		}
		files, _ := s.goFiles(dir)
		nodes = append(nodes, codeNode{id: id, name: name, does: fmt.Sprintf("Go package %s · %d files", dir, len(files)), files: []string{dir}, dir: dir})
	}
	edges := []codeEdge{}
	if module == "" {
		return nodes, edges
	}
	for _, dir := range dirs {
		files, _ := s.goFiles(dir)
		seen := map[string]bool{}
		for _, name := range files {
			f := s.parse(dir+"/"+name, parser.ImportsOnly)
			if f == nil {
				continue
			}
			for _, spec := range f.Imports {
				target, _ := strconv.Unquote(spec.Path.Value)
				if !strings.HasPrefix(target, module+"/") {
					continue
				}
				to, ok := index[strings.TrimPrefix(target, module+"/")]
				if ok && to != index[dir] && !seen[to] {
					seen[to] = true
					edges = append(edges, codeEdge{index[dir], to, "imports"})
				}
			}
		}
	}
	return nodes, edges
}

// fileLevel: one node per file of a package; a Go file points at the files
// declaring the package-level names it uses.
func (s *codeScan) fileLevel(dir string, names []string, gocode bool) ([]codeNode, []codeEdge) {
	nodes := []codeNode{}
	ids := map[string]string{}
	for i, name := range names {
		id := "file-" + strconv.Itoa(i+1)
		ids[name] = id
		does := "File " + dir + "/" + name
		if dir == "" {
			does = "File " + name
		}
		nodes = append(nodes, codeNode{id: id, name: path.Base(name), does: does, files: []string{path.Join(dir, name)}})
	}
	edges := []codeEdge{}
	if !gocode {
		return nodes, edges
	}
	declared := map[string]string{}
	parsed := map[string]*ast.File{}
	for _, name := range names {
		f := s.parse(path.Join(dir, name), 0)
		if f == nil {
			continue
		}
		parsed[name] = f
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.Name != "init" && d.Name.Name != "_" {
					declared[d.Name.Name] = name
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch sp := spec.(type) {
					case *ast.TypeSpec:
						declared[sp.Name.Name] = name
					case *ast.ValueSpec:
						for _, n := range sp.Names {
							if n.Name != "_" {
								declared[n.Name] = name
							}
						}
					}
				}
			}
		}
	}
	for _, name := range names {
		f := parsed[name]
		if f == nil {
			continue
		}
		uses := map[string][]string{}
		var visit func(ast.Node) bool
		visit = func(n ast.Node) bool {
			// x.Name selects a field, method or another package's name,
			// never this package's declaration: look only at x.
			if sel, ok := n.(*ast.SelectorExpr); ok {
				ast.Inspect(sel.X, visit)
				return false
			}
			ident, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			if owner, ok := declared[ident.Name]; ok && owner != name {
				list := uses[owner]
				if len(list) < 3 && !hasString(list, ident.Name) {
					uses[owner] = append(list, ident.Name)
				} else if !hasString(list, ident.Name) && !hasString(list, "…") {
					uses[owner] = append(list, "…")
				}
			}
			return true
		}
		for _, decl := range f.Decls {
			ast.Inspect(decl, visit)
		}
		owners := make([]string, 0, len(uses))
		for owner := range uses {
			owners = append(owners, owner)
		}
		sort.Strings(owners)
		for _, owner := range owners {
			edges = append(edges, codeEdge{ids[name], ids[owner], "uses " + strings.Join(uses[owner], ", ")})
		}
	}
	return nodes, edges
}

func hasString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// DeriveCodeView builds the derived level of one part: its packages, or the
// files of one of its packages when dir names it (or when it has only one).
// dir must be one of the part's own packages.
func DeriveCodeView(ctx context.Context, root, part string, paths []string, dir string) (*CodeView, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("code view needs an absolute project root")
	}
	deadline := time.Now().Add(codeViewTimeLimit)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	s := &codeScan{root: root, deadline: deadline}
	dirs, explicit := s.packages(paths)
	view := &CodeView{Derived: true, Part: part, Dir: dir}
	var nodes []codeNode
	var edges []codeEdge
	switch {
	case dir != "":
		if !hasString(dirs, dir) {
			return nil, errors.New("the folder is not one of this part's packages")
		}
		names, gocode := s.goFiles(dir)
		nodes, edges = s.fileLevel(dir, names, gocode)
		view.Kind = "files"
	case len(dirs) >= 2:
		nodes, edges = s.packageLevel(dirs)
		view.Kind = "packages"
		view.Drill = map[string]string{}
		for _, n := range nodes {
			view.Drill[n.id] = n.dir
		}
	case len(dirs) == 1:
		names, gocode := s.goFiles(dirs[0])
		nodes, edges = s.fileLevel(dirs[0], names, gocode)
		view.Kind, view.Dir = "files", dirs[0]
	case len(explicit) > 0:
		if len(explicit) > codeViewMaxFiles {
			explicit, s.cut = explicit[:codeViewMaxFiles], true
		}
		nodes, edges = s.fileLevel("", explicit, false)
		view.Kind = "files"
	default:
		view.Kind = "empty"
	}
	view.Truncated = s.cut
	components, connections := []any{}, []any{}
	for _, n := range nodes {
		components = append(components, map[string]any{"id": n.id, "name": n.name, "responsibility": n.does, "technology": "", "files": n.files})
	}
	for i, e := range edges {
		connections = append(connections, map[string]any{"id": "use-" + strconv.Itoa(i+1), "source": e.source, "target": e.target, "label": e.label, "protocol": ""})
	}
	view.Architecture = map[string]any{"title": part, "purpose": "Derived from code, not reviewed", "components": components, "connections": connections}
	if len(nodes) == 0 {
		return view, nil
	}
	// Connected files get the layered drawing; files nothing links sit in a
	// compact grid below it, so a large package does not become one tall column.
	linked := map[string]bool{}
	for _, e := range edges {
		linked[e.source], linked[e.target] = true, true
	}
	drawn := map[string]any{"title": part, "purpose": "Derived from code, not reviewed", "components": []any{}, "connections": connections}
	var loose []codeNode
	for i, n := range nodes {
		if linked[n.id] {
			drawn["components"] = append(drawn["components"].([]any), components[i])
		} else {
			loose = append(loose, n)
		}
	}
	layout := Layout{Version: "derived-v1", Width: 160, Height: 72, Nodes: []Node{}, Edges: []Edge{}}
	if len(drawn["components"].([]any)) > 0 {
		raw, err := canonicalJSON(drawn)
		if err != nil {
			return nil, err
		}
		layouts, err := GenericLayouts(raw)
		if err != nil {
			return nil, err
		}
		for _, l := range layouts {
			layout = l
		}
	}
	placeLoose(&layout, loose)
	view.Layout = &layout
	return view, nil
}

// placeLoose puts unlinked nodes in rows below the drawing.
func placeLoose(l *Layout, loose []codeNode) {
	if len(loose) == 0 {
		return
	}
	const w, h, gap, margin = 200.0, 56.0, 20.0, 72.0
	columns := max(3, int(math.Ceil(math.Sqrt(float64(len(loose))*2))))
	if fit := int((l.Width - 2*margin + gap) / (w + gap)); fit > columns {
		columns = fit
	}
	top := margin
	if len(l.Nodes) > 0 {
		top = l.Height
	}
	for i, n := range loose {
		x := margin + float64(i%columns)*(w+gap)
		y := top + float64(i/columns)*(h+gap)
		l.Nodes = append(l.Nodes, Node{ID: n.id, X: x, Y: y, Width: w, Height: h})
		l.Width = math.Max(l.Width, x+w+margin)
		l.Height = math.Max(l.Height, y+h+margin)
	}
}
