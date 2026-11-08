//go:build !governed

package statepath

import "testing"

func TestExtremeNamespaceSelected(t *testing.T) {
	if DirName != ".marshal" || Name != "marshal" || OtherDirName != ".marshal-extreme" {
		t.Fatal("Marshal namespace not isolated")
	}
}
