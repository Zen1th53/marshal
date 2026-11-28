package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"strings"
	"testing"
)

// Recovery exceptions are confined to replacing plan definitions. Ordinary
// execution, restart and resume use the lifecycle helper without exceptions.
func TestTaskRecoveryEntryPoints(t *testing.T) {
	want := map[string]int{
		"marshal_lifecycle.go ApplyAmendDraftBound ResetTaskForAmendment":   2,
		"marshal_lifecycle.go ApplyAmendDraftBound RestoreAmendedTaskState": 1,
	}
	got := map[string]int{}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if sel.Sel.Name == "ResetTaskForAmendment" || sel.Sel.Name == "RestoreAmendedTaskState" {
					got[file+" "+fn.Name.Name+" "+sel.Sel.Name]++
				}
				return true
			})
		}
	}
	if !maps.Equal(got, want) {
		t.Fatalf("task recovery entry points changed: got %v, want %v", got, want)
	}
}
