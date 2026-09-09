package cloud_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests are about the shape of the tree rather than the behaviour of one
// function. The pack requires that every entry path capable of requesting ULTRA
// goes through the canonical gate, and that is a property of where the decision
// is made, not of what any single call returns. A unit test cannot see a second
// call site appearing in a package it does not import, so this looks directly.

// repoRoot walks up to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the module root")
	return ""
}

// goFiles lists non-test Go files under root, skipping vendor and testdata.
func goFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case "vendor", "testdata", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return files
}

// goalintake.Confirm is the one function that decides whether ULTRA may
// delegate a confirmation. If a second call site appears, it must be given the
// canonical gate's mode and policy, so this test fails on any new one and asks
// whoever added it to wire it deliberately.
func TestConfirmHasASingleCallSite(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()

	var sites []string
	for _, path := range goFiles(t, filepath.Join(root, "internal")) {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Confirm" {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "goalintake" {
				return true
			}
			rel, _ := filepath.Rel(root, path)
			sites = append(sites, rel+":"+fset.Position(call.Pos()).String())
			return true
		})
	}

	if len(sites) != 1 {
		t.Fatalf("goalintake.Confirm must have exactly one call site so ULTRA is decided in one place; found %d:\n  %s",
			len(sites), strings.Join(sites, "\n  "))
	}
	if !strings.Contains(sites[0], "internal/cli/goal.go") {
		t.Fatalf("the ULTRA decision moved to %s; the gate wiring must move with it", sites[0])
	}
}

// The delegation policy must be built by the gate, never by a literal. A struct
// literal setting Entitled would be exactly the local flag the design refuses
// to depend on, and it would compile perfectly well.
func TestNoHandwrittenEntitlement(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()

	var offenders []string
	for _, path := range goFiles(t, filepath.Join(root, "internal")) {
		// The gate itself is the one place allowed to construct the policy.
		if strings.Contains(path, filepath.Join("internal", "cloud")) {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "DelegationPolicy" {
				return true
			}
			// An empty literal is the safe default and stays allowed.
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || key.Name != "Entitled" {
					continue
				}
				// Setting it from a call is how the gate supplies it; setting it
				// from a literal true is how a bypass would look.
				if ident, ok := kv.Value.(*ast.Ident); ok && ident.Name == "true" {
					rel, _ := filepath.Rel(root, path)
					offenders = append(offenders, rel+":"+fset.Position(kv.Pos()).String())
				}
			}
			return true
		})
	}

	if len(offenders) > 0 {
		t.Fatalf("DelegationPolicy.Entitled is hardcoded true, which bypasses the Cloud gate:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// The constitutional gate's EntitlementValid must come from the runtime, not
// from a caller's request. This checks the one production assignment is the
// runtime's own.
func TestEntitlementValidIsRuntimeSupplied(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "internal", "app", "constitution_runtime.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "state.EntitlementValid = s.runtime.ULTRAEntitled()") {
		t.Fatal("the constitution runtime no longer derives EntitlementValid from the ULTRA gate; " +
			"a caller-supplied value would let a surface grant itself ULTRA")
	}
}
