package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommunityArchitectureReceiptCLIImportsOnly(t *testing.T) {
	repo := cliRepo(t)
	for _, args := range [][]string{
		{"architecture", "receipt", "sign", "receipt.json"},
		{"architecture", "receipt", "enroll", "receipt.json"},
		{"architecture", "approve", "receipt.json"},
	} {
		var out, errout bytes.Buffer
		if code := Execute(context.Background(), repo.Path(), args, strings.NewReader(""), &out, &errout); code == 0 {
			t.Fatalf("private command accepted %v", args)
		}
	}
	file := filepath.Join(repo.Path(), "receipt.json")
	if err := os.WriteFile(file, []byte(`{"challenge":{},"Signature":""}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errout bytes.Buffer
	if code := Execute(context.Background(), repo.Path(), []string{"architecture", "receipt", "import", file}, strings.NewReader(""), &out, &errout); code == 0 {
		t.Fatal("invalid original-wire receipt imported")
	}
	if _, err := os.Stat(filepath.Join(repo.Path(), ".marshal", "state.db")); !os.IsNotExist(err) {
		t.Fatalf("invalid import created a database: %v", err)
	}
}
