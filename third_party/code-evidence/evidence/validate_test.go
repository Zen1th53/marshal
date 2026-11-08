package evidence

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateExpandedEvidence(t *testing.T) {
	for _, kind := range []string{"body", "callers", "later_snapshot", "revoked", "purged"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			write := func(name, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write("go.mod", "module example.com/test\n\ngo 1.25\n")
			write("balance.go", "package test\nfunc Balance() int {\n return 1\n}\n")
			e, err := Open(ctx, Options{Root: root, StoreDir: t.TempDir(), Lexical: "go"})
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			d, err := e.Provide(ctx, ProvideRequest{Task: "Balance", Recipe: RecipeNameLocator, Budget: 8000})
			if err != nil {
				t.Fatal(err)
			}
			if len(d.Entries) == 0 {
				t.Fatal("no locator entries")
			}
			if len(d.Receipt.Predicates) != 0 {
				t.Fatal("locator unexpectedly has predicates")
			}
			if kind == "later_snapshot" {
				write("balance.go", "package test\nfunc Balance() int {\n return 2\n}\n")
			}
			view := "source"
			if kind == "callers" {
				view = "callers"
			}
			selection := d.Entries[0].Symbol
			if kind == "revoked" || kind == "purged" {
				write("other.go", "package test\nfunc Other() int {\n return Balance()\n}\n")
				selection = "other.go"
			}
			x, err := e.Expand(ctx, ExpandRequest{Receipt: d.Receipt.ID, Select: selection, View: view, Budget: 8000})
			if err != nil {
				t.Fatal(err)
			}
			if x.Delivery == "" {
				t.Fatal("no recorded delivery")
			}
			if kind == "callers" {
				write("caller.go", "package test\nfunc Caller() int { return Balance() }\n")
			} else if kind == "revoked" || kind == "purged" {
				ledger, err := e.Deliveries(ctx, d.Receipt.ID)
				if err != nil {
					t.Fatal(err)
				}
				var blob string
				for _, dl := range ledger.Entries {
					for _, sp := range dl.Spans {
						blob = sp.Blob
					}
				}
				if blob == "" {
					t.Fatal("no expanded blob")
				}
				if _, err := e.store.Revoke(ctx, blob, e.now()); err != nil {
					t.Fatal(err)
				}
				if kind == "purged" {
					if _, err := e.store.Purge(ctx, blob); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				write("balance.go", "package test\nfunc Balance() int {\n return 3\n}\n")
			}
			for _, full := range []bool{false, true} {
				rep, err := e.Validate(ctx, ValidateRequest{Receipts: []string{d.Receipt.ID}, Full: full})
				if err != nil {
					t.Fatal(err)
				}
				changed := false
				for _, rv := range rep.Receipts {
					if rv.Status == "evidence_revoked" {
						changed = true
					}
					for _, sp := range rv.Spans {
						if sp.Verdict == SpanChanged || sp.Verdict == SpanUnknown {
							changed = true
						}
					}
					for _, p := range rv.Predicates {
						if p.Verdict == PredInvalidated {
							changed = true
						}
					}
				}
				if !changed {
					t.Fatalf("expanded %s falsely holds (full=%v): %+v", kind, full, rep)
				}
			}
		})
	}
}
