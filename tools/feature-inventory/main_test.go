package inventory

import (
	"errors"
	"fmt"
	"go/build"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"go.yaml.in/yaml/v3"
)

// frozenPending and frozenFeatures live in their own reviewed baseline files.

func checkFrozenFeatures(inv Inventory, frozen []string) error {
	ids := map[string]bool{}
	for _, f := range inv.Features {
		ids[f.ID] = true
	}
	for _, l := range inv.Later {
		ids[l.ID] = true
	}
	var problems []error
	for _, id := range frozen {
		if !ids[id] {
			problems = append(problems, fmt.Errorf("%s: frozen feature must exist in features or later", id))
		}
	}
	return errors.Join(problems...)
}

func TestFeatureInventoryBaseline(t *testing.T) {
	repo := os.DirFS("../..")
	data, err := fs.ReadFile(repo, "docs/features.yaml")
	if err != nil {
		t.Fatal(err)
	}
	inv, err := Load(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(Validate(repo, inv, frozenPending), checkFrozenFeatures(inv, frozenFeatures)); err != nil {
		t.Fatal(err)
	}
}

func fixture() (fstest.MapFS, Inventory) {
	repo := fstest.MapFS{
		"internal/tui/commands.go":          {Data: []byte("package tui\n// feature-inventory: F01\nfunc Help() {}\nconst help = `/help Reference`\n")},
		"internal/tui/commands_test.go":     {Data: []byte("package tui\nimport \"testing\"\nfunc TestHelp(t *testing.T) {}\n")},
		"docs/guide.md":                     {Data: []byte("# Reference")},
		"internal/webboard/page/index.html": {Data: []byte(`<a href="#work">Work</a><section id="s-work">Work reference</section>`)},
		"internal/webboard/page/app.js":     {Data: []byte(`var sections = ["work"];`)},
	}
	inv := Inventory{Version: 1, Features: []Feature{{ID: "F01", Name: "Reference", Editions: []string{"default", "governed"},
		Sources: []string{"internal/tui/commands.go:Help"}, Tests: []string{"internal/tui.TestHelp"}, Docs: []string{"docs/guide.md"},
		TUI: []Surface{{RouteOrCommand: "/help", Anchor: "Reference", Kind: "view"}},
		Web: []Surface{{RouteOrCommand: "#work", Anchor: "Work reference", Kind: "view"}},
	}}}
	return repo, inv
}
func wantFailure(t *testing.T, repo fs.FS, inv Inventory, frozen []Pending, fragments ...string) {
	t.Helper()
	err := Validate(repo, inv, frozen)
	if err == nil {
		t.Fatal("expected inventory failure")
	}
	for _, fragment := range fragments {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error %q does not contain %q", err, fragment)
		}
	}
}
func TestFeatureWithoutSurfaceFails(t *testing.T) {
	repo, inv := fixture()
	if err := Validate(repo, inv, nil); err != nil {
		t.Fatal(err)
	}
	inv.Features[0].TUI = nil
	wantFailure(t, repo, inv, nil, "F01", "default", "governed", "no reachable surface")
}
func TestComingSoonFails(t *testing.T) {
	repo, inv := fixture()
	inv.Features[0].Web = []Surface{{RouteOrCommand: "#work", Anchor: "Work reference", Kind: "view"}}
	if err := Validate(repo, inv, nil); err != nil {
		t.Fatal(err)
	}
	repo["internal/webboard/page/index.html"].Data = append(repo["internal/webboard/page/index.html"].Data, []byte("<p>Coming soon</p>")...)
	// A valid TUI does not excuse a declared Web stub.
	wantFailure(t, repo, inv, nil, "F01", "Coming soon")
}
func TestOrphanSourceKeyFails(t *testing.T) {
	repo, inv := fixture()
	repo["internal/tui/extra.go"] = &fstest.MapFile{Data: []byte("package tui\n// feature-inventory: NEW-CONTROL\n")}
	wantFailure(t, repo, inv, nil, "NEW-CONTROL", "orphan source key")
	p := Pending{ID: "NEW-CONTROL", Missing: "both", Source: "internal/tui/extra.go"}
	inv.Pending = []Pending{p}
	if err := Validate(repo, inv, []Pending{p}); err != nil {
		t.Fatal(err)
	}
}
func TestNewPendingExceptionFails(t *testing.T) {
	repo, inv := fixture()
	p := Pending{ID: "F01", Missing: "web", Source: "internal/tui/commands.go"}
	inv.Pending = []Pending{p}
	inv.Features[0].Web = nil
	wantFailure(t, repo, inv, nil, "F01", "new or expanded pending exception")
	if err := Validate(repo, inv, []Pending{p}); err != nil {
		t.Fatal(err)
	}
	inv.Pending[0].Missing = "both"
	wantFailure(t, repo, inv, []Pending{p}, "expanded pending")
	inv.Pending = nil
	inv.Features[0].Web = []Surface{{RouteOrCommand: "#work", Anchor: "Work reference", Kind: "view"}}
	if err := Validate(repo, inv, []Pending{p}); err != nil {
		t.Fatalf("deleting pending after adding coverage must pass: %v", err)
	}
}

func TestRemovedFeatureFails(t *testing.T) {
	repo, inv := fixture()
	frozen := []string{"F01"}
	// Neither a source marker nor a pending row should be needed to catch deletion.
	delete(repo, "internal/tui/commands.go")
	inv.Features = nil
	err := checkFrozenFeatures(inv, frozen)
	if err == nil || !strings.Contains(err.Error(), "F01: frozen feature must exist in features or later") {
		t.Fatalf("expected removed feature failure, got %v", err)
	}
	inv.Later = []Later{{ID: "F01", Decision: "Approved deferral"}}
	if err := checkFrozenFeatures(inv, frozen); err != nil {
		t.Fatalf("explicit deferral must pass: %v", err)
	}
	inv.Later = nil
	if err := checkFrozenFeatures(inv, nil); err != nil {
		t.Fatalf("explicit frozen list removal must pass: %v", err)
	}
}

func TestPendingMissingMustMatchSurfaces(t *testing.T) {
	for _, missing := range []string{"web", "tui", "both"} {
		t.Run(missing, func(t *testing.T) {
			repo, inv := fixture()
			p := Pending{ID: "F01", Missing: missing, Source: "internal/tui/commands.go"}
			inv.Pending = []Pending{p}
			wantFailure(t, repo, inv, []Pending{p}, "F01", "stale pending")
			if missing == "web" || missing == "both" {
				inv.Features[0].Web = nil
			}
			if missing == "tui" || missing == "both" {
				inv.Features[0].TUI = nil
			}
			if err := Validate(repo, inv, []Pending{p}); err != nil {
				t.Fatal(err)
			}
			if missing == "web" {
				inv.Features[0].TUI = nil
				wantFailure(t, repo, inv, []Pending{p}, "default tui", "governed tui", "no reachable surface")
			}
			if missing == "tui" {
				inv.Features[0].Web = nil
				wantFailure(t, repo, inv, []Pending{p}, "default web", "governed web", "no reachable surface")
			}
		})
	}
	for _, missing := range []string{"", "invalid", "web,tui"} {
		repo, inv := fixture()
		p := Pending{ID: "F01", Missing: missing, Source: "internal/tui/commands.go"}
		inv.Pending = []Pending{p}
		wantFailure(t, repo, inv, []Pending{p}, "pending missing must be web, tui or both")
	}
}

func TestInventoryValidation(t *testing.T) {
	cases := []struct {
		name, fragment string
		mutate         func(fstest.MapFS, *Inventory)
	}{
		{"version", "version", func(_ fstest.MapFS, i *Inventory) { i.Version = 2 }},
		{"duplicate", "duplicate feature", func(_ fstest.MapFS, i *Inventory) { i.Features = append(i.Features, i.Features[0]) }},
		{"edition", "invalid or duplicate edition", func(_ fstest.MapFS, i *Inventory) { i.Features[0].Editions = []string{"unknown"} }},
		{"missing source", "source", func(r fstest.MapFS, _ *Inventory) { delete(r, "internal/tui/commands.go") }},
		{"missing symbol", "source symbol", func(_ fstest.MapFS, i *Inventory) { i.Features[0].Sources[0] += "Missing" }},
		{"missing doc", "document", func(r fstest.MapFS, _ *Inventory) { delete(r, "docs/guide.md") }},
		{"missing test", "test not found", func(r fstest.MapFS, _ *Inventory) { delete(r, "internal/tui/commands_test.go") }},
		{"anchor", "anchor", func(_ fstest.MapFS, i *Inventory) { i.Features[0].TUI[0].Anchor = "Missing" }},
		{"kind", "invalid surface kind", func(_ fstest.MapFS, i *Inventory) { i.Features[0].TUI[0].Kind = "soon" }},
		{"locked reason", "requires a reason", func(_ fstest.MapFS, i *Inventory) { i.Features[0].TUI[0].Kind = "locked" }},
		{"locked rendering", "not rendered", func(_ fstest.MapFS, i *Inventory) {
			i.Features[0].TUI[0].Kind = "locked"
			i.Features[0].TUI[0].Reason = "Policy denies access"
		}},
		{"edition source", "unavailable in governed", func(r fstest.MapFS, _ *Inventory) {
			r["internal/tui/commands.go"].Data = append([]byte("//go:build !governed\n\n"), r["internal/tui/commands.go"].Data...)
		}},
		{"path escape", "repo-relative", func(_ fstest.MapFS, i *Inventory) { i.Features[0].Docs = []string{"../outside.md"} }},
		{"later no approval", "approval decision", func(_ fstest.MapFS, i *Inventory) { i.Later = []Later{{ID: "F01"}} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { r, i := fixture(); tc.mutate(r, &i); wantFailure(t, r, i, nil, tc.fragment) })
	}
}
func TestWebRouteMustBeReachable(t *testing.T) {
	for _, file := range []string{"internal/webboard/page/index.html", "internal/webboard/page/app.js"} {
		t.Run(file, func(t *testing.T) {
			repo, inv := fixture()
			inv.Features[0].Web = []Surface{{RouteOrCommand: "#work", Anchor: "Work reference", Kind: "view"}}
			if err := Validate(repo, inv, nil); err != nil {
				t.Fatal(err)
			}
			repo[file].Data = []byte(strings.ReplaceAll(string(repo[file].Data), "work", "missing"))
			wantFailure(t, repo, inv, nil, "F01", "web route")
		})
	}
}
func TestWebAnchorSpecificity(t *testing.T) {
	for _, tc := range []struct {
		anchor string
		valid  bool
	}{
		{"send", false},
		{"function ()", false},
		{"(){};:[]!_._._._", false},
		{"return false;", false},
		{"function () {}", false},
		{"function send()", true},
		{"Work reference", true},
	} {
		t.Run(tc.anchor, func(t *testing.T) {
			repo, inv := fixture()
			repo["internal/webboard/page/app.js"].Data = append(repo["internal/webboard/page/app.js"].Data, []byte("\n"+tc.anchor)...)
			inv.Features[0].Web[0].Anchor = tc.anchor
			err := Validate(repo, inv, nil)
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "web anchor must be at least 12 characters") {
				t.Fatalf("expected weak anchor failure, got %v", err)
			}
		})
	}
}

func TestSurfaceBuildTargetIsPortable(t *testing.T) {
	original := build.Default
	t.Cleanup(func() { build.Default = original })
	build.Default.GOOS = "windows"
	build.Default.GOARCH = "arm64"
	repo, inv := fixture()
	const source = "internal/tui/commands_linux_amd64.go"
	repo[source] = repo["internal/tui/commands.go"]
	delete(repo, "internal/tui/commands.go")
	inv.Features[0].Sources = []string{source + ":Help"}
	if err := Validate(repo, inv, nil); err != nil {
		t.Fatal(err)
	}
	repo[source].Data = append([]byte("//go:build !governed\n\n"), repo[source].Data...)
	wantFailure(t, repo, inv, nil, "unavailable in governed")
}

func TestPendingStillChecksPresentSurfaceInEachEdition(t *testing.T) {
	repo, inv := fixture()
	p := Pending{ID: "F01", Missing: "web", Source: "internal/tui/commands.go"}
	inv.Pending = []Pending{p}
	inv.Features[0].Web = nil
	repo["internal/tui/commands.go"].Data = append([]byte("//go:build !governed\n\n"), repo["internal/tui/commands.go"].Data...)
	wantFailure(t, repo, inv, []Pending{p}, "governed tui", "unavailable in governed")
}

func TestLoadRejectsInvalidSchema(t *testing.T) {
	for _, data := range []string{
		"version: 1\nunknown: true\n", "version: 1\nversion: 2\n", "version: 1\n---\nversion: 1\n", "version: 1\nfeatures: {}\n", "version: 1\nfeatures:\n - id: F01\n   editions: perhaps\n",
	} {
		if _, err := Load(strings.NewReader(data)); err == nil {
			t.Errorf("accepted invalid YAML: %s", data)
		}
	}
	_, inv := fixture()
	data, err := yaml.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(strings.NewReader(string(data))); err != nil {
		t.Fatalf("valid schema: %v", err)
	}
	for _, field := range []string{"tests:", "editions:", "web:", "pending: []", "later: []"} {
		// Renaming a required field is rejected even if its old zero value
		// would otherwise look like a valid absent surface.
		changed := strings.Replace(string(data), field, "", 1)
		if changed == string(data) {
			t.Fatalf("fixture lacks %q", field)
		}
		if _, err := Load(strings.NewReader(changed)); err == nil {
			t.Errorf("accepted missing field %s", field)
		}
	}
}

// This check is structural and portable: it reads the workflow with the same
// YAML library, models its push filters, and requires an unconditional package
// check in the job consumed by the existing required verification job.
func TestInventoryOnlyPushRunsRequiredGate(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := requiredGate(data); err != nil {
		t.Fatal(err)
	}
	for name, replacement := range map[string][2]string{
		"docs ignored":              {"paths-ignore:", "paths-ignore:\n      - 'docs/**'"},
		"wrong branch":              {"- main", "- release"},
		"conditional package check": {"- name: Validate feature inventory in both editions", "- if: false\n        name: Validate feature inventory in both editions"},
		"no package check":          {"go test ./tools/feature-inventory/", "go version"},
		"no governed check":         {"go test -tags governed ./tools/feature-inventory/", "go version"},
		"not required":              {"needs: [checks, test]", "needs: [test]"},
	} {
		t.Run(name, func(t *testing.T) {
			changed := strings.Replace(string(data), replacement[0], replacement[1], 1)
			if changed == string(data) {
				t.Fatal("workflow mutation did not apply")
			}
			if err := requiredGate([]byte(changed)); err == nil {
				t.Fatal("broken workflow passed")
			}
		})
	}
}
func requiredGate(data []byte) error {
	type trigger struct {
		Branches       []string `yaml:"branches"`
		PathsIgnore    []string `yaml:"paths-ignore"`
		Paths          []string `yaml:"paths"`
		BranchesIgnore []string `yaml:"branches-ignore"`
	}
	type step struct {
		Run string `yaml:"run"`
		If  any    `yaml:"if"`
	}
	type job struct {
		If    any      `yaml:"if"`
		Needs []string `yaml:"needs"`
		Steps []step   `yaml:"steps"`
	}
	var w struct {
		On   map[string]trigger `yaml:"on"`
		Jobs map[string]job     `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &w); err != nil {
		return err
	}
	push, ok := w.On["push"]
	if !ok || !slices.Contains(push.Branches, "main") || len(push.BranchesIgnore) != 0 {
		return fmt.Errorf("push to main must run the gate")
	}
	if _, ok := w.On["pull_request"]; !ok {
		return fmt.Errorf("pull requests must run the gate")
	}
	for _, file := range []string{"docs/features.yaml", "tools/feature-inventory/inventory.go", "tools/feature-inventory/main_test.go"} {
		for _, pattern := range push.PathsIgnore {
			if workflowMatch(pattern, file) {
				return fmt.Errorf("push ignores %s", file)
			}
		}
		if len(push.Paths) > 0 {
			matched := false
			for _, pattern := range push.Paths {
				if strings.HasPrefix(pattern, "!") {
					if workflowMatch(pattern[1:], file) {
						matched = false
					}
				} else if workflowMatch(pattern, file) {
					matched = true
				}
			}
			if !matched {
				return fmt.Errorf("push paths excludes %s", file)
			}
		}
	}
	for name, j := range w.Jobs {
		if j.If != nil {
			continue
		}
		ordinary, governed := false, false
		for _, s := range j.Steps {
			if s.If != nil {
				continue
			}
			for _, line := range strings.Split(s.Run, "\n") {
				line = strings.TrimSpace(line)
				if line == "go test ./tools/feature-inventory/" {
					ordinary = true
				}
				if line == "go test -tags governed ./tools/feature-inventory/" {
					governed = true
				}
			}
		}
		if ordinary && governed && slices.Contains(w.Jobs["verify"].Needs, name) {
			return nil
		}
	}
	return fmt.Errorf("required verify job must depend on an unconditional inventory gate for both editions")
}

// GitHub path filters use ** across directory boundaries, unlike path.Match.
func workflowMatch(pattern, file string) bool {
	if pattern == "" {
		return file == ""
	}
	if strings.HasPrefix(pattern, "**") {
		if workflowMatch(pattern[2:], file) {
			return true
		}
		for n := 0; n < len(file); n++ {
			if workflowMatch(pattern[2:], file[n+1:]) {
				return true
			}
		}
		return false
	}
	if pattern[0] == '*' {
		if workflowMatch(pattern[1:], file) {
			return true
		}
		for n := 0; n < len(file) && file[n] != '/'; n++ {
			if workflowMatch(pattern[1:], file[n+1:]) {
				return true
			}
		}
		return false
	}
	if len(file) == 0 {
		return false
	}
	if pattern[0] == '?' && file[0] != '/' {
		return workflowMatch(pattern[1:], file[1:])
	}
	return pattern[0] == file[0] && workflowMatch(pattern[1:], file[1:])
}
