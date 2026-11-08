package architecturecodec_test

import (
	"crypto/ed25519"
	"encoding/json"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
)

func TestArchitectureCommandApprovalEditionBoundary(t *testing.T) {
	g, raw := syntheticReceipt(t)
	r, err := architecturecodec.DecodeSignedArchitectureReceipt(raw)
	if err != nil {
		t.Fatal(err)
	}
	key := ed25519.NewKeyFromSeed(bytes32(9))
	g.Policy = "private_extreme"
	g.CommandIssuer = key.Public().(ed25519.PublicKey)
	r.Challenge.Binding.ArchitectureTarget = g.ArchitectureTarget
	r.Challenge.Binding.Origin = "owner_command"
	r.Challenge.Binding.ModelSelfApproval = true
	r.Challenge.Binding.OperatorInstructionRef = "chat:operator-message-1"
	sign := func() {
		architecturecodec.BindArchitectureChallenge(&r.Challenge)
		payload, err := json.Marshal(r.Challenge)
		if err != nil {
			t.Fatal(err)
		}
		r.Signature = ed25519.Sign(key, append([]byte("marshal.architecture.receipt.v1\x00"), payload...))
	}
	sign()
	if err := r.Verify(g, time.Now()); (err == nil) != commandApprovalEdition {
		t.Fatalf("command acceptance: %v, Marshal=%v", err, commandApprovalEdition)
	}
	if !commandApprovalEdition {
		return
	}
	valid := r
	for _, change := range []string{"instruction", "model flag", "revision", "digest", "project", "signature", "community"} {
		t.Run(change, func(t *testing.T) {
			r = valid
			target := g
			switch change {
			case "instruction":
				r.Challenge.Binding.OperatorInstructionRef = ""
				sign()
			case "model flag":
				r.Challenge.Binding.ModelSelfApproval = false
				sign()
			case "revision":
				r.Challenge.Binding.Revision++
			case "digest":
				r.Challenge.Binding.Digest = string(make([]byte, 64))
			case "project":
				target.ProjectID = "another-project"
			case "signature":
				r.Signature = make([]byte, ed25519.SignatureSize)
			case "community":
				target.Policy = "community"
			}
			if err := r.Verify(target, time.Now()); err == nil {
				t.Fatal("invalid command receipt accepted")
			}
		})
	}
}
