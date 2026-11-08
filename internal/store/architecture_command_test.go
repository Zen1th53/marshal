package store

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
	"github.com/Zen1th53/marshal/internal/statepath"
)

func TestArchitectureCommandReceiverEditionBoundary(t *testing.T) {
	for _, mutation := range []string{"none", "signature", "instruction", "model flag", "source", "revision", "project", "store"} {
		t.Run(mutation, func(t *testing.T) {
			s, receipt, fact := registeredArchitectureOwner(t)
			c := &receipt.Challenge
			c.Binding.Origin = "owner_command"
			c.Binding.ModelSelfApproval = true
			c.Binding.OperatorInstructionRef = "operator:message-1"
			if mutation == "instruction" {
				c.Binding.OperatorInstructionRef = ""
			}
			if mutation == "model flag" {
				c.Binding.ModelSelfApproval = false
			}
			if mutation == "project" {
				c.Binding.ProjectID = "PROJECT-foreign"
			}
			if mutation == "store" {
				c.Binding.StorePath += ".foreign"
			}
			BindArchitectureChallenge(c)
			key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{18}, ed25519.SeedSize))
			payload, err := architectureSignedBytes("marshal.architecture.receipt.v1", *c)
			if err != nil {
				t.Fatal(err)
			}
			receipt.Signature = ed25519.Sign(key, payload)
			if mutation == "signature" {
				receipt.Signature = make([]byte, ed25519.SignatureSize)
			}
			if mutation == "source" {
				if err := os.WriteFile(filepath.Join(c.Binding.Root, "bound.go"), []byte("package changed\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mutation == "revision" {
				path := statepath.Path(c.Binding.Root, "architecture", "review", "state.json")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, bytes.Replace(raw, []byte(`"revision":1`), []byte(`"revision":2`), 1), 0600); err != nil {
					t.Fatal(err)
				}
			}
			accepted, err := s.ImportObservedArchitectureReceipt(t.Context(), receipt)
			want := architecturecodec.ModelArchitectureApprovalAllowed() && mutation == "none"
			if (err == nil) != want {
				t.Fatalf("acceptance=%+v err=%v, want success=%v", accepted, err, want)
			}
			if !want {
				if queryInt(t, s.db, "SELECT count(*) FROM architecture_accepted_revisions") != 0 {
					t.Fatal("refused command wrote acceptance")
				}
				return
			}
			if !accepted.ModelSelfApproval || accepted.OperatorInstructionRef != c.Binding.OperatorInstructionRef {
				t.Fatal("receipt omitted operator command metadata")
			}
			if !s.ArchitectureFactAvailable(t.Context(), accepted.ProjectID, fact) {
				t.Fatal("command-accepted fact unavailable")
			}
			raw, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := architecturecodec.DecodeSignedArchitectureReceipt(raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.ImportObservedArchitectureReceipt(t.Context(), decoded); err != nil {
				t.Fatalf("exact receipt retry: %v", err)
			}
		})
	}
}
