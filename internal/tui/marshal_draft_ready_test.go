package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMarshalDraftReadyNeedsDraftAndMarker(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, marshalDraftRelativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if marshalDraftReady(root) {
		t.Fatal("ready without a draft")
	}
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if marshalDraftReady(root) {
		t.Fatal("an unpublished draft counted as ready")
	}
	if err := os.WriteFile(path+".ready", []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !marshalDraftReady(root) {
		t.Fatal("a published draft was not ready")
	}
}
