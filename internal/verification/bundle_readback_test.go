package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

// buildReadbackEnvelope mirrors the canonical evidence-bundle export: a manifest
// plus the exact payloads needed to replay its integrity check. It never loads
// raw provider transcripts — only the reviewed session and evidence metadata.
func buildReadbackEnvelope(t *testing.T, s Session) BundleEnvelope {
	t.Helper()
	payloads := map[string][]byte{}
	body, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	payloads["verification.json"] = body
	for _, ev := range s.Evidence {
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(ev.ID))
		payloads["evidence/"+hex.EncodeToString(sum[:])+".json"] = raw
	}
	var entries []BundleEntry
	for path, payload := range payloads {
		sum := sha256.Sum256(payload)
		entries = append(entries, BundleEntry{Path: path, Digest: hex.EncodeToString(sum[:]), Size: int64(len(payload))})
	}
	bundle, err := BuildEvidenceBundle("bundle-"+s.ID+"-"+strconv.FormatInt(s.Version, 10), s.ID, s.Binding, entries, s.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	return BundleEnvelope{Bundle: bundle, Payloads: payloads}
}

// TestVerificationBundleBuildExportReadback proves a Process 06 evidence bundle
// survives export (a JSON round-trip) and readback with its manifest digest and
// binding intact, and that neither payload nor binding tamper survives readback.
func TestVerificationBundleBuildExportReadback(t *testing.T) {
	session := fixture(time.Now())
	envelope := buildReadbackEnvelope(t, session)
	if err := envelope.Bundle.Verify(envelope.Payloads, session.Binding); err != nil {
		t.Fatalf("built bundle failed verification: %v", err)
	}

	exported, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var readback BundleEnvelope
	if err = json.Unmarshal(exported, &readback); err != nil {
		t.Fatal(err)
	}
	if readback.Bundle.ManifestDigest != envelope.Bundle.ManifestDigest {
		t.Fatalf("manifest digest changed on readback: %q != %q",
			readback.Bundle.ManifestDigest, envelope.Bundle.ManifestDigest)
	}
	if err = readback.Bundle.Verify(readback.Payloads, session.Binding); err != nil {
		t.Fatalf("readback bundle failed verification: %v", err)
	}

	// Payload tamper must not survive readback.
	readback.Payloads["verification.json"] = []byte(`{"forged":true}`)
	if err = readback.Bundle.Verify(readback.Payloads, session.Binding); err != ErrTampered {
		t.Fatalf("payload tamper survived readback: %v", err)
	}

	// A foreign binding must refuse the same exported bundle.
	foreign := session.Binding
	foreign.TreeDigest = "other-tree"
	if err = envelope.Bundle.Verify(envelope.Payloads, foreign); err != ErrBindingMismatch {
		t.Fatalf("foreign binding accepted the exported bundle: %v", err)
	}
}
