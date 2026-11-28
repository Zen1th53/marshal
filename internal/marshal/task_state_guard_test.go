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
			const target = "github.com/Zen1th53/marshal/internal/marshal"
			fset := token.NewFileSet()
			imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) { return os.Open(exports[path]) })
			for _, p := range packages {
				relevant := p.ImportPath == target
				for _, path := range p.Imports {
					relevant = relevant || path == target
				}
				if !relevant {
					continue
				}
				var files []*ast.File
				for _, name := range p.GoFiles {
					f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, 0)
					if err != nil {
						t.Fatal(err)
					}
					files = append(files, f)
				}
				info := &types.Info{Selections: map[*ast.SelectorExpr]*types.Selection{}}
				conf := types.Config{Importer: imp}
				if _, err := conf.Check(p.ImportPath, fset, files, info); err != nil {
					t.Fatal(err)
				}
				for _, f := range files {
					for _, d := range f.Decls {
						fn, ok := d.(*ast.FuncDecl)
						if !ok {
							continue
						}
						if p.ImportPath == target && filepath.Base(fset.Position(fn.Pos()).Filename) == "task_state.go" && fn.Name.Name == "setTaskState" {
							continue
						}
						ast.Inspect(fn, func(n ast.Node) bool {
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
							ast.Inspect(fn, func(parent ast.Node) bool {
								switch x := parent.(type) {
								case *ast.AssignStmt:
									for _, lhs := range x.Lhs {
										if lhs == sel {
											t.Errorf("%s: task state write outside helper", fset.Position(sel.Pos()))
										}
									}
								case *ast.UnaryExpr:
									if x.Op == token.AND && x.X == sel {
										t.Errorf("%s: task state address escapes helper", fset.Position(sel.Pos()))
									}
								}
								return true
							})
							return true
						})
					}
				}
			}
		})
	}
}
