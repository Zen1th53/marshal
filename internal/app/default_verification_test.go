package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestDefaultVerificationRunner(t *testing.T) {
	root := t.TempDir()
	_, err := DefaultVerificationCommand(root)
	if !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "marshal verify --") {
		t.Fatalf("missing runner: %v", err)
	}
	path := filepath.Join(root, "conformance", "runner.py")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := DefaultVerificationCommand(root); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("directory accepted as runner: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("raise Exception('must not run during resolution')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := DefaultVerificationCommand(root)
	if err != nil || !reflect.DeepEqual(got, []string{"python", "conformance/runner.py", "validate-pack"}) {
		t.Fatalf("runner=%v err=%v", got, err)
	}
}

func TestRuntimeVerifyMissingRunnerFailsBeforeExecution(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	_, err = runtime.Verify(t.Context(), VerifyRequest{})
	if !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "conformance/runner.py") {
		t.Fatalf("missing runner error=%v", err)
	}
}
