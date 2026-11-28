package marshal

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Type information catches aliases, indexed tasks and variable-valued writes,
// without confusing execution tasks or run states with this task lifecycle.
func TestTaskStateWritesUseHelper(t *testing.T) {
	for _, tags := range []string{"", "governed"} {
		t.Run(tags, func(t *testing.T) {
			args := []string{"list", "-deps", "-export", "-json"}
			if tags != "" {
				args = append(args, "-tags", tags)
			}
			args = append(args, "./...")
			cmd := exec.Command("go", args...)
			cmd.Dir = "../.."
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			data, err := cmd.Output()
			if err != nil {
				t.Fatalf("load source: %v: %s", err, stderr.String())
			}
			type pkg struct {
				Dir, ImportPath, Export string
				GoFiles, Imports        []string
			}
			var packages []pkg
			exports := map[string]string{}
			dec := json.NewDecoder(bytes.NewReader(data))
			for {
				var p pkg
				err := dec.Decode(&p)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				packages = append(packages, p)
				exports[p.ImportPath] = p.Export
			}
			// go list omits directories whose files are all excluded by the host
			// platform or tags. Discover those directories independently as well.
			root, err := filepath.Abs("../..")
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			for _, p := range packages {
				seen[p.Dir] = true
			}
			err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					if path != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor" || entry.Name() == "testdata" || entry.Name() == "SCRATCH") {
						return filepath.SkipDir
					}
					return nil
				}
				if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return nil
				}
				dir := filepath.Dir(path)
				if !seen[dir] {
					rel, err := filepath.Rel(root, dir)
					if err != nil {
						return err
					}
					packages = append(packages, pkg{Dir: dir, ImportPath: "github.com/Zen1th53/marshal/" + filepath.ToSlash(rel)})
					seen[dir] = true
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			const target = "github.com/Zen1th53/marshal/internal/marshal"
			fset := token.NewFileSet()
			imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) { return os.Open(exports[path]) })
			for _, p := range packages {
				if !strings.HasPrefix(p.ImportPath, "github.com/Zen1th53/marshal/") {
					continue
				}
				var files, active []*ast.File
				names, err := filepath.Glob(filepath.Join(p.Dir, "*.go"))
				if err != nil {
					t.Fatal(err)
				}
				for _, full := range names {
					name := filepath.Base(full)
					if strings.HasSuffix(name, "_test.go") {
						continue
					}
					f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, 0)
					if err != nil {
						t.Fatal(err)
					}
					files = append(files, f)
					for _, selected := range p.GoFiles {
						if selected == name {
							active = append(active, f)
							break
						}
					}
				}
				info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
				conf := types.Config{Importer: imp, Error: func(error) {}}
				// Alternate build files can declare the same symbols. Retain partial type
				// information while scanning every source file, independent of build tags.
				_, _ = conf.Check(p.ImportPath, fset, active, info)
				_, _ = conf.Check(p.ImportPath, fset, files, info)
				for _, f := range files {
					checkTaskStateWrites(fset, info, p.ImportPath, f, t.Errorf)
				}
			}
		})
	}
}

// Whole-struct copies remain allowed. Store snapshot validation rejects illegal
// results, including terminal resets after a plan version change.
func unparenTaskState(expr ast.Expr) ast.Expr {
	for {
		p, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = p.X
	}
}

func checkTaskStateWrites(fset *token.FileSet, info *types.Info, packagePath string, f *ast.File, report func(string, ...any)) {
	const target = "github.com/Zen1th53/marshal/internal/marshal"
	for _, d := range f.Decls {
		fn, _ := d.(*ast.FuncDecl)
		if fn != nil && packagePath == target && filepath.Base(fset.Position(fn.Pos()).Filename) == "task_state.go" && fn.Name.Name == "setTaskState" {
			continue
		}
		ast.Inspect(d, func(n ast.Node) bool {
			if lit, ok := n.(*ast.CompositeLit); ok {
				typ := info.TypeOf(lit)
				var named *types.Named
				if typ != nil {
					named, _ = types.Unalias(typ).(*types.Named)
				}
				isTask := named != nil && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == target && named.Obj().Name() == "Task"
				// Syntax fallback covers alternate declarations with incomplete type information.
				if id, ok := lit.Type.(*ast.Ident); ok && packagePath == target && id.Name == "Task" {
					isTask = true
				}
				if sel, ok := lit.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Task" {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "marshal" {
						isTask = true
					}
				}
				if isTask {
					for _, elt := range lit.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok {
							if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "State" {
								file := filepath.Base(fset.Position(lit.Pos()).Filename)
								allowed := fn != nil && packagePath == "github.com/Zen1th53/marshal/internal/app" && (file == "marshal_bind_plan.go" && fn.Name.Name == "BindApprovedPlan" || file == "marshal_canonical_approval.go" && fn.Name.Name == "canonicalBoundRun")
								value, ok := kv.Value.(*ast.SelectorExpr)
								if !allowed || !ok || value.Sel.Name != "Queued" {
									report("%s: task state literal outside queued creation sites", fset.Position(kv.Pos()))
								}
							}
						}
					}
				}
			}
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			selection := info.Selections[sel]
			if selection == nil || selection.Obj().Name() != "State" {
				return true
			}
			named, ok := selection.Obj().Type().(*types.Named)
			if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != target || named.Obj().Name() != "TaskState" {
				return true
			}
			// Inspect parents to distinguish reads from writes and address escapes.
			ast.Inspect(d, func(parent ast.Node) bool {
				switch x := parent.(type) {
				case *ast.AssignStmt:
					for _, lhs := range x.Lhs {
						if unparenTaskState(lhs) == sel {
							report("%s: task state write outside helper", fset.Position(sel.Pos()))
						}
					}
				case *ast.UnaryExpr:
					if x.Op == token.AND && unparenTaskState(x.X) == sel {
						report("%s: task state address escapes helper", fset.Position(sel.Pos()))
					}
				}
				return true
			})
			return true
		})
	}
}

func TestTaskStateGuardRecognizesBypasses(t *testing.T) {
	for _, statement := range []string{
		`var bad = func(t *Task) { (t.State) = Queued }`,
		`var bad = func(t *Task) { _ = &((t.State)) }`,
		`var bad = func() Task { return Task{State: Queued} }`,
		`type Alias = Task; var bad = Alias{State: Queued}`,
	} {
		t.Run(statement, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "extra_windows.go", "//go:build windows && special\n\npackage marshal\ntype TaskState string; const Queued TaskState = \"queued\"; type Task struct { State TaskState };\n"+statement, 0)
			if err != nil {
				t.Fatal(err)
			}
			info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
			var conf types.Config
			if _, err := conf.Check("github.com/Zen1th53/marshal/internal/marshal", fset, []*ast.File{f}, info); err != nil {
				t.Fatal(err)
			}
			reports := 0
			checkTaskStateWrites(fset, info, "github.com/Zen1th53/marshal/internal/marshal", f, func(string, ...any) { reports++ })
			if reports == 0 {
				t.Fatal("guard missed state write")
			}
		})
	}
}
