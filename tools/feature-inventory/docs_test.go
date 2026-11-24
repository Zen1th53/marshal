package inventory

import (
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestConfigurationDocsMatchCatalogue regenerates the settings table of the
// current edition and fails when docs/configuration.md differs.
func TestConfigurationDocsMatchCatalogue(t *testing.T) {
	args := []string{"run"}
	if governedBuild {
		args = append(args, "-tags", "governed")
	}
	args = append(args, "./tools/feature-inventory/cmd/settingsdocs", "-check", "docs/configuration.md")
	cmd := exec.Command("go", args...)
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestInventoryDocsAnchorsResolve requires every documentation reference in the
// inventory, including heading fragments, to resolve to a page and heading.
func TestInventoryDocsAnchorsResolve(t *testing.T) {
	repo := os.DirFS("../..")
	data, err := fs.ReadFile(repo, "docs/features.yaml")
	if err != nil {
		t.Fatal(err)
	}
	inv, err := Load(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	fragments := 0
	for _, f := range inv.Features {
		for _, doc := range f.Docs {
			if err := checkDoc(repo, doc); err != nil {
				t.Errorf("%s: %s: %v", f.ID, doc, err)
			}
			if strings.Contains(doc, "#") {
				fragments++
			}
		}
	}
	if fragments == 0 {
		t.Fatal("no documentation reference points at a heading")
	}
	if err := checkDoc(repo, "docs/configuration.md#no-such-heading"); err == nil {
		t.Fatal("a missing heading must fail")
	}
}
