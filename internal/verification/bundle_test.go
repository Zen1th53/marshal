package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestEvidenceBundleDetectsManifestPayloadAndBindingTamper(t *testing.T) {
	b := fixture(time.Now()).Binding
	payload := []byte("evidence")
	h := sha256.Sum256(payload)
	bundle, err := BuildEvidenceBundle("bundle", "verify", b, []BundleEntry{{Path: "logs/test.txt", Digest: hex.EncodeToString(h[:]), Size: int64(len(payload))}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"logs/test.txt": payload}
	if err := bundle.Verify(files, b); err != nil {
		t.Fatal(err)
	}
	files["logs/test.txt"] = []byte("forged")
	if err := bundle.Verify(files, b); err != ErrTampered {
		t.Fatalf("payload tamper: %v", err)
	}
	files["logs/test.txt"] = payload
	bundle.Entries[0].Path = "logs/other.txt"
	if err := bundle.Verify(files, b); err != ErrTampered {
		t.Fatalf("manifest tamper: %v", err)
	}
}
