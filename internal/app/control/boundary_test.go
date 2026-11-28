package control

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Report all source files, including both editions, without enforcing the
// eventual client boundary while the extraction is incremental.
func TestTUIImportBoundaryReport(t *testing.T) {
	root := filepath.Join("..", "..", "tui")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, im := range f.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			for _, name := range []string{"store", "worker", "processgroup", "sandbox", "netpolicy"} {
				if strings.HasSuffix(p, "/internal/"+name) {
					t.Logf("WARN: %s imports %s", filepath.ToSlash(path), p)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
