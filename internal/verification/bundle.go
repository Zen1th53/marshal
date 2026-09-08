package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

type BundleEntry struct {
	Path, Digest string
	Size         int64
}
type EvidenceBundle struct {
	ID             string
	Binding        Binding
	VerificationID string
	Entries        []BundleEntry
	CreatedAt      time.Time
	ManifestDigest string
}

// BundleEnvelope carries the manifest and the exact payloads needed to replay
// its integrity check. JSON represents payload bytes as base64 strings.
type BundleEnvelope struct {
	Bundle   EvidenceBundle    `json:"bundle"`
	Payloads map[string][]byte `json:"payloads"`
}

func BuildEvidenceBundle(id, verificationID string, binding Binding, entries []BundleEntry, now time.Time) (EvidenceBundle, error) {
	if id == "" || verificationID == "" || ValidateBinding(binding) != nil {
		return EvidenceBundle{}, fmt.Errorf("%w: evidence bundle identity", ErrInvalid)
	}
	copyEntries := append([]BundleEntry(nil), entries...)
	sort.Slice(copyEntries, func(i, j int) bool { return copyEntries[i].Path < copyEntries[j].Path })
	for i, e := range copyEntries {
		if e.Path == "" || e.Digest == "" || e.Size < 0 || (i > 0 && copyEntries[i-1].Path == e.Path) {
			return EvidenceBundle{}, fmt.Errorf("%w: evidence bundle entry", ErrInvalid)
		}
	}
	b := EvidenceBundle{ID: id, Binding: binding, VerificationID: verificationID, Entries: copyEntries, CreatedAt: now.UTC()}
	b.ManifestDigest, _ = digest(b)
	return b, nil
}

func (b EvidenceBundle) Verify(payloads map[string][]byte, current Binding) error {
	stored := b.ManifestDigest
	b.ManifestDigest = ""
	got, err := digest(b)
	if err != nil {
		return err
	}
	if stored == "" || stored != got {
		return ErrTampered
	}
	if b.Binding != current {
		return ErrBindingMismatch
	}
	if len(payloads) != len(b.Entries) {
		return ErrTampered
	}
	for _, entry := range b.Entries {
		payload, ok := payloads[entry.Path]
		if !ok || int64(len(payload)) != entry.Size {
			return ErrTampered
		}
		h := sha256.Sum256(payload)
		if hex.EncodeToString(h[:]) != entry.Digest {
			return ErrTampered
		}
	}
	return nil
}
