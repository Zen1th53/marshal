//go:build governed

package statepath

import "testing"

func TestDefaultNamespacePreserved(t *testing.T) {
	if DirName != ".marshal" || Name != "marshal" || OtherDirName != ".marshal-extreme" {
		t.Fatal("Community namespace changed")
	}
}
