//go:build !governed

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
)

func requiredMarshalBinaryDigest(path string) (string, string, error) {
	if !filepath.IsAbs(path) {
		return "", "", projectcontext.Changed("project_context_marshal_identity_extreme.go:19")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", err
	}
	f, err := os.Open(resolved)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	before, err := f.Stat()
	const limit = 512 * 1024 * 1024
	if err != nil || !before.Mode().IsRegular() || before.Size() > limit {
		return "", "", projectcontext.Changed("project_context_marshal_identity_extreme.go:33")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, limit+1))
	if err != nil {
		return "", "", err
	}
	after, err := f.Stat()
	if err != nil || n != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return "", "", projectcontext.Changed("project_context_marshal_identity_extreme.go:42")
	}
	return resolved, "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func (m *MarshalCLI) prepareRequiredMarshalBinary(path string) error {
	p := m.requiredTurnPreparation
	if p == nil || p.cli != m || p.runtime != m.runtime {
		return projectcontext.ErrMissing
	}
	resolved, digest, err := requiredMarshalBinaryDigest(path)
	if err != nil {
		return err
	}
	p.binaryCommandPath, p.binaryPath, p.binaryDigest = path, resolved, digest
	return nil
}

func (m *MarshalCLI) validateRequiredMarshalBinary() error {
	p := m.requiredTurnPreparation
	if p == nil || p.binaryPath == "" || p.binaryDigest == "" {
		return projectcontext.ErrMissing
	}
	path, digest, err := requiredMarshalBinaryDigest(p.binaryCommandPath)
	if err != nil || path != p.binaryPath || digest != p.binaryDigest {
		return projectcontext.Changed("project_context_marshal_identity_extreme.go:67")
	}
	return nil
}

// Only an unambiguous typed native Claude final-response field is observed.
// Requested selectors and model text inside structured output convey no model
// identity. Other providers, absent metadata and conflicts remain UNKNOWN.
func requiredMarshalObservedModel(provider string, raw []byte) string {
	if provider != "claude" {
		return ""
	}
	var envelope map[string]json.RawMessage
	if proposalmcp.DecodeStrict(raw, &envelope) != nil {
		return ""
	}
	var usage map[string]json.RawMessage
	if json.Unmarshal(envelope["modelUsage"], &usage) != nil || len(usage) != 1 {
		return ""
	}
	for key, value := range usage {
		var detail map[string]json.RawMessage
		if json.Unmarshal(value, &detail) != nil {
			return ""
		}
		var canonical string
		if json.Unmarshal(detail["canonicalModel"], &canonical) != nil || canonical != key || len(canonical) == 0 || len(canonical) > 128 {
			return ""
		}
		for _, c := range canonical {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '/') {
				return ""
			}
		}
		return canonical
	}
	return ""
}
