//go:build !governed

package app

import (
	"encoding/json"
	"strings"

	"github.com/Zen1th53/marshal/internal/verification"
)

func runtimeCheckOutputName(check verification.RuntimeCheckProof) string {
	return "check-output-" + strings.TrimPrefix(check.OutputArtifact.Digest, "sha256:") + ".json"
}

func addRuntimeCheckOutputs(payloads map[string][]byte, proof *verification.RuntimeProof) error {
	if proof == nil {
		return nil
	}
	for _, check := range proof.Checks {
		if check.OutputArtifact == nil {
			continue
		}
		data, err := readRuntimeCheckOutput(check)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(string(data))
		if err != nil {
			return err
		}
		payloads[runtimeCheckOutputName(check)] = raw
	}
	return nil
}

func runtimeCheckEvidenceKind(check verification.RuntimeCheckProof) string {
	switch check.Isolation {
	case "host-process-only":
		return "approved-host-runtime-check"
	case "bwrap":
		return "approved-confined-runtime-check"
	default:
		return "approved-runtime-check-isolation-unknown"
	}
}
