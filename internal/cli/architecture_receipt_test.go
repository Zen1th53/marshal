package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The receipt receiver was never enrollable, so no receipt verb is offered.
func TestArchitectureReceiptVerbsAreRejected(t *testing.T) {
	repo := cliRepo(t)
	file := filepath.Join(repo.Path(), "receipt.json")
	if err := os.WriteFile(file, []byte(`{"challenge":{},"Signature":""}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"architecture", "receipt", "import", file},
		{"architecture", "receipt", "sign", "receipt.json"},
		{"architecture", "receipt", "enroll", "receipt.json"},
		{"architecture", "approve", "receipt.json"},
	} {
		var out, errout bytes.Buffer
		if code := Execute(context.Background(), repo.Path(), args, strings.NewReader(""), &out, &errout); code == 0 {
			t.Fatalf("receipt command accepted %v", args)
		}
	}
	if _, err := os.Stat(filepath.Join(repo.Path(), ".marshal", "state.db")); !os.IsNotExist(err) {
		t.Fatalf("rejected receipt command created a database: %v", err)
	}
}
