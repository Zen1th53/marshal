package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestTaskSnapshotSQLWritersStayGuarded(t *testing.T) {
	// Run writes validate the prior snapshot inside the revision transaction.
	// The atomic task upsert only projects the run validated immediately above it.
	want := map[string]int{"marshal_atomic.go saveMarshalStateTx": 1}
	got := map[string]int{}
	writes := regexp.MustCompile(`(?i)\b(?:INSERT\s+(?:OR\s+\w+\s+)?INTO|UPDATE|REPLACE\s+INTO)\s+[\x60"\[]?marshal_(?:tasks|runs)\b`)
	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "third_party" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				literal, ok := n.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				sql, err := strconv.Unquote(literal.Value)
				if err == nil && writes.MatchString(sql) {
					key := filepath.Base(path) + " " + fn.Name.Name
					got[key]++
					if want[key] == 0 {
						t.Errorf("%s: task snapshot SQL must use guarded persistence", fset.Position(literal.Pos()))
					}
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(got, want) {
		t.Fatalf("task snapshot SQL writers changed: got %v, want %v", got, want)
	}
}
