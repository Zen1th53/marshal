package projectknowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestKnowledgeGoBindingsKeepCentralPrivacyAndMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"identifier", "secret := existing; _ = secret", true},
		{"typed call", "token := acquire(ctx); _ = token", true},
		{"field", "_ = Config{secret: existing}", true},
		{"range binding", "for _, token := range existing { _ = token }", true},
		{"bearer prefix", "token := strings.TrimPrefix(authHeader, \"Bearer \"); _ = token", true},
		{"direct short secret", "secret := \"x\"; _ = secret", false},
		{"literal in call", "token := identity(\"hunter2\"); _ = token", false},
		{"concatenated literal", "password := \"hun\" + \"ter2\"; _ = password", false},
		{"literal field", "_ = Config{secret: \"x\"}", false},
		{"different prefix", "token := strings.TrimPrefix(authHeader, \"hunter2\"); _ = token", false},
		{"literal first prefix argument", "token := strings.TrimPrefix(\"hunter2\", \"Bearer \"); _ = token", false},
		{"credential call literal", "token := acquire(\"ghp_" + strings.Repeat("x", 36) + "\"); _ = token", false},
		{"comment remains", "secret := existing; _ = secret // password=hunter2", false},
		{"unrelated literal remains", "secret := existing; _ = secret; _ = \"password=hunter2\"", false},
		{"protocol marker remains", "secret := existing; _ = secret // MARSHAL PROTOCOL", false},
		{"invalid Go", "secret := existing; _ = secret; BROKEN(", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte("package example\nfunc CoreDeclaration(){ " + tc.body + " }\n")
			if safeContent(data) {
				t.Fatal("fixture no longer exercises the prose classifier refusal")
			}
			if got := safeSourceContent("core.go", data); got != tc.want {
				t.Fatalf("metadata eligibility = %v, want %v", got, tc.want)
			}
			if safeSourceContent("core.txt", data) {
				t.Fatal("Go syntax exemption leaked to plain text")
			}
		})
	}
	root, idx := fixture(t)
	write(t, root, "pkg/core.go", "package pkg\nfunc CoreDeclaration(){ secret := existing; _ = secret }\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "typed bindings fixture")
	if _, err := idx.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := query(t, idx, "symbol", "CoreDeclaration", "")
	if len(got.Items) != 1 || got.Items[0].Path != "pkg/core.go" || got.Items[0].Line != 2 {
		t.Fatal("typed binding lost source-derived definition")
	}
	write(t, root, "pkg/core.go", "package pkg\nfunc CoreDeclaration(){ secret := \"x\"; _ = secret }\n")
	if _, err := idx.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(query(t, idx, "symbol", "CoreDeclaration", "").Items) != 0 {
		t.Fatal("literal credential retained previous definition")
	}
}

func TestKnowledgePrivacyCacheSeparatesGoAndProse(t *testing.T) {
	data := []byte("package p\nfunc Core(){ token := existing; _ = token }\n")
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	root := t.TempDir()
	if safe, _ := contentVerdict(root, hash, data, "core.go"); !safe {
		t.Fatal("typed metadata source rejected")
	}
	if safe, reused := contentVerdict(root, hash, data, "core.md"); safe || reused {
		t.Fatal("Go binding exemption was reused for prose")
	}
}

func TestKnowledgeApprovedBriefScopeAndTestLinks(t *testing.T) {
	_, idx := fixture(t)
	if _, err := idx.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err := idx.Query(t.Context(), Query{Mode: "brief", Paths: []string{"pkg/a.go"}, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	definition, imported, test := false, false, false
	for _, item := range result.Items {
		if item.Path != "pkg/a.go" && item.Path != "pkg/a_test.go" {
			t.Fatal("approved brief widened beyond source and adjacent test candidates")
		}
		definition = definition || item.Name == "Helper" && item.Kind == "function"
		imported = imported || item.Target == "fmt" && item.Kind == "import"
		test = test || item.Name == "TestHelper" && item.Kind == "test"
	}
	if !definition || !imported || !test {
		t.Fatal("brief lacks definition/import/test provenance")
	}
	for _, paths := range [][]string{nil, {"."}, {"../outside.go"}, {".marshal-extreme/state.go"}, {"internal/constitution/protocol.go"}} {
		if _, err := idx.Query(t.Context(), Query{Mode: "brief", Paths: paths, Limit: 100}); err == nil {
			t.Fatal("invalid/private scope accepted")
		}
	}
}
