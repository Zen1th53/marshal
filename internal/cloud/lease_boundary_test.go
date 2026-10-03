package cloud

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLeasePackageDoesNotIssueLeases(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate lease package")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "lease.go"))
	if err != nil {
		t.Fatalf("read lease package: %v", err)
	}
	if bytes.Contains(source, []byte("func SignLease(")) {
		t.Fatal("lease issuance must not be part of the production cloud package")
	}
}
