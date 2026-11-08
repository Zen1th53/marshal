//go:build !governed

package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func TestRequiredMarshalObservedModelUsesNativeMetadataOnly(t *testing.T) {
	for _, tc := range []struct{ name, provider, raw, expected string }{
		{"native", "claude", `{"modelUsage":{"claude-sonnet-5-5":{"canonicalModel":"claude-sonnet-5-5"}}}`, "claude-sonnet-5-5"},
		{"claimed", "claude", `{"structured_output":{"model":"claude-sonnet-5-5"},"result":"model claude-sonnet-5-5"}`, ""},
		{"conflicting", "claude", `{"modelUsage":{"a":{"canonicalModel":"a"},"b":{"canonicalModel":"b"}}}`, ""},
		{"mismatch", "claude", `{"modelUsage":{"sonnet":{"canonicalModel":"claude-sonnet-5-5"}}}`, ""},
		{"duplicate", "claude", `{"modelUsage":{"a":{"canonicalModel":"a","canonicalModel":"b"}}}`, ""},
		{"alias", "claude", `{"ModelUsage":{"a":{"canonicalModel":"a"}}}`, ""},
		{"foreign", "codex", `{"modelUsage":{"a":{"canonicalModel":"a"}}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := requiredMarshalObservedModel(tc.provider, []byte(tc.raw)); got != tc.expected {
				t.Fatalf("observed %q, expected %q", got, tc.expected)
			}
		})
	}
}

func TestRequiredMarshalBinaryIdentityRejectsChangesAndLinkRetarget(t *testing.T) {
	for _, retarget := range []bool{false, true} {
		t.Run(map[bool]string{false: "content", true: "symlink"}[retarget], func(t *testing.T) {
			root := t.TempDir()
			binary, link := filepath.Join(root, "binary"), filepath.Join(root, "cli")
			if err := os.WriteFile(binary, []byte("first"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(binary, link); err != nil {
				t.Fatal(err)
			}
			r := &Runtime{}
			m := &MarshalCLI{runtime: r}
			m.requiredTurnPreparation = &requiredMarshalTurnPreparation{runtime: r, cli: m}
			if err := m.prepareRequiredMarshalBinary(link); err != nil {
				t.Fatal(err)
			}
			if err := m.validateRequiredMarshalBinary(); err != nil {
				t.Fatal(err)
			}
			if retarget {
				other := filepath.Join(root, "other")
				if err := os.WriteFile(other, []byte("first"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, link); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(binary, []byte("second"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := m.validateRequiredMarshalBinary(); !errors.Is(err, projectcontext.ErrChanged) {
				t.Fatal("changed CLI identity accepted", err)
			}
		})
	}
}
