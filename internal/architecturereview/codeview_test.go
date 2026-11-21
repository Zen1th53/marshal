package architecturereview

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func codeRepo(t *testing.T) string {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":              "module example.com/m\n",
		"svc/api/api.go":      "package api\nimport \"example.com/m/svc/store\"\nfunc Serve() { store.Save(Item{}) }\n",
		"svc/api/item.go":     "package api\ntype Item struct{}\n",
		"svc/api/api_test.go": "package api\n",
		"svc/store/store.go":  "package store\nfunc Save(any) {}\n",
		"svc/store/lone.go":   "package store\nvar unused = 1\n",
		"svc/.hidden/x.go":    "package hidden\n",
		"svc/testdata/t.go":   "package testdata\n",
		"docs/readme.md":      "hello\n",
	})
	return root
}

// A part's code level lists its packages and the imports between them; one
// package opens to its files and which declarations each file uses.
func TestDerivedCodeViewPackagesAndFiles(t *testing.T) {
	root := codeRepo(t)
	view, err := DeriveCodeView(t.Context(), root, "svc", []string{"svc"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !view.Derived || view.Kind != "packages" || view.Architecture["purpose"] != "Derived from code, not reviewed" {
		t.Fatalf("not a derived package level: %+v", view)
	}
	names := map[string]string{}
	for _, c := range view.Architecture["components"].([]any) {
		m := c.(map[string]any)
		names[m["id"].(string)] = m["name"].(string)
	}
	if len(names) != 2 || !strings.Contains(fmt.Sprint(names), "api") || !strings.Contains(fmt.Sprint(names), "store") {
		t.Fatalf("packages = %v, want api and store only (hidden and testdata skipped)", names)
	}
	edges := view.Architecture["connections"].([]any)
	if len(edges) != 1 || names[edges[0].(map[string]any)["source"].(string)] != "api" {
		t.Fatalf("imports = %v, want api -> store", edges)
	}
	if len(view.Drill) != 2 || view.Layout == nil {
		t.Fatalf("package level must be drillable and drawn: %+v", view)
	}
	files, err := DeriveCodeView(t.Context(), root, "svc", []string{"svc"}, "svc/api")
	if err != nil {
		t.Fatal(err)
	}
	if files.Kind != "files" || len(files.Architecture["components"].([]any)) != 2 {
		t.Fatalf("files of svc/api = %+v (tests are left out)", files.Architecture["components"])
	}
	uses := files.Architecture["connections"].([]any)
	if len(uses) != 1 || !strings.Contains(uses[0].(map[string]any)["label"].(string), "Item") {
		t.Fatalf("api.go should use Item from item.go: %v", uses)
	}
	if len(files.Layout.Nodes) != 2 {
		t.Fatalf("drawing has %d files", len(files.Layout.Nodes))
	}
}

// Contract paths and browser folders never leave the project root.
func TestDerivedCodeViewIsConfined(t *testing.T) {
	root := codeRepo(t)
	outside := t.TempDir()
	writeTree(t, outside, map[string]string{"secret/s.go": "package secret\n"})
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{{"../" + filepath.Base(outside)}, {outside}, {"link"}, {".git"}, {"svc/*"}, {""}} {
		view, err := DeriveCodeView(t.Context(), root, "p", paths, "")
		if err != nil {
			t.Fatal(err)
		}
		if view.Kind != "empty" {
			t.Errorf("paths %v produced a %s level", paths, view.Kind)
		}
	}
	for _, dir := range []string{"..", "svc/../../x", "docs", "/etc", "link"} {
		if _, err := DeriveCodeView(t.Context(), root, "p", []string{"svc"}, dir); err == nil {
			t.Errorf("folder %q outside the part's packages was opened", dir)
		}
	}
}

// Every bound holds: package and file caps mark the level truncated.
func TestDerivedCodeViewIsBounded(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"go.mod": "module example.com/m\n"}
	for i := 0; i < codeViewMaxPackages+15; i++ {
		files[fmt.Sprintf("big/p%02d/a.go", i)] = fmt.Sprintf("package p%02d\n", i)
	}
	for i := 0; i < codeViewMaxFiles+25; i++ {
		files[fmt.Sprintf("wide/f%03d.go", i)] = fmt.Sprintf("package wide\nfunc F%d() {}\n", i)
	}
	writeTree(t, root, files)
	view, err := DeriveCodeView(t.Context(), root, "big", []string{"big"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(view.Architecture["components"].([]any)); n != codeViewMaxPackages || !view.Truncated {
		t.Fatalf("%d packages, truncated %v; want the %d cap", n, view.Truncated, codeViewMaxPackages)
	}
	wide, err := DeriveCodeView(t.Context(), root, "wide", []string{"wide"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(wide.Architecture["components"].([]any)); n != codeViewMaxFiles || !wide.Truncated || wide.Kind != "files" {
		t.Fatalf("%d files, truncated %v, kind %s; want the %d cap", n, wide.Truncated, wide.Kind, codeViewMaxFiles)
	}
	if empty, err := DeriveCodeView(t.Context(), root, "none", nil, ""); err != nil || empty.Kind != "empty" || empty.Layout != nil {
		t.Fatalf("a part without paths must be an empty level: %+v %v", empty, err)
	}
}

// The endpoint needs the token, serves only parts without declared
// sub-parts, and never changes the reviewed state, its digests or Accept.
func TestCodeViewEndpointIsReadOnlyAndOutsideAccept(t *testing.T) {
	root := codeRepo(t)
	raw := json.RawMessage(`{"title":"T","purpose":"P","components":[{"id":"svc","name":"svc","responsibility":"r","technology":"","files":["svc"]},{"id":"box","name":"box","responsibility":"r","technology":"","files":[]},{"id":"inner","name":"inner","responsibility":"r","technology":"","files":[],"parent":"box"}],"connections":[{"id":"c","source":"svc","target":"inner","label":"l","protocol":""}]}`)
	r, err := New(Config{Context: t.Context(), Project: "fixture", ArchitectureID: "architecture-fixture", StateDir: filepath.Join(t.TempDir(), "state"), Proposal: raw, CodeRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := r.State()
	token := "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(r.Handler(token))
	defer server.Close()
	get := func(query string, auth bool) (int, map[string]any) {
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/codeview?"+query, nil)
		if auth {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(res.Body).Decode(&body)
		return res.StatusCode, body
	}
	if status, _ := get("part=svc", false); status != http.StatusForbidden {
		t.Fatalf("code view without token: %d", status)
	}
	status, body := get("part=svc", true)
	if status != http.StatusOK || body["derived"] != true || body["kind"] != "packages" {
		t.Fatalf("code view: %d %v", status, body)
	}
	if status, _ := get("part=box", true); status != http.StatusNotFound {
		t.Fatalf("a part with sub-parts must use its reviewed sub-architecture, got %d", status)
	}
	if status, _ := get("part=nothing", true); status != http.StatusNotFound {
		t.Fatalf("unknown part: %d", status)
	}
	if status, _ := get("part=svc&dir=docs", true); status != http.StatusNotFound {
		t.Fatalf("foreign folder: %d", status)
	}
	if status, body := get("part=inner", true); status != http.StatusOK || body["kind"] != "empty" {
		t.Fatalf("a part without paths must be an empty level: %d %v", status, body)
	}
	after, _ := r.State()
	if string(before) != string(after) {
		t.Fatal("reading a code view changed the review state")
	}
	if err := applyFixture(t, r, requestFixtureFor(t, r, "approve")); err != nil {
		t.Fatalf("Accept after browsing code views: %v", err)
	}
	accepted := stateFixture(t, r)
	if !strings.Contains(fmt.Sprint(accepted["architecture"]), "svc") || strings.Contains(string(mustJSON(t, accepted)), "pkg-1") {
		t.Fatal("the accepted state must be the reviewed contract only")
	}
}

func requestFixtureFor(t *testing.T, r *Review, action string) map[string]any {
	request := requestFixture(t, r, action)
	delete(request, "target")
	delete(request, "comment")
	delete(request, "request_type")
	return request
}

func mustJSON(t *testing.T, v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The owner's nested parts get a drawing at every depth.
func TestNestedPartsHaveADrawingAtEveryDepth(t *testing.T) {
	views, err := GenericLayouts(marshalShapeFixture(false))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"architecture", "sub:architecture-review", "sub:review-server"} {
		if len(views[key].Nodes) == 0 {
			t.Errorf("no drawing for %s", key)
		}
	}
	ids := map[string]bool{}
	for _, n := range views["sub:review-server"].Nodes {
		ids[n.ID] = true
	}
	if !ids["token-guard"] || !ids["review-api"] || !ids["review-state"] || len(ids) != 3 {
		t.Fatalf("second level parts = %v", ids)
	}
}
