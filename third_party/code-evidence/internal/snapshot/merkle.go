package snapshot

import "crypto/sha256"

// MerkleRoot computes an RFC 6962-style Merkle tree hash over leaves:
// leaf = H(0x00 || data), node = H(0x01 || left || right), with the split at
// the largest power of two smaller than n. The empty tree hashes to H("").
func MerkleRoot(leaves [][]byte) [32]byte {
	if len(leaves) == 0 {
		return sha256.Sum256(nil)
	}
	hashes := make([][32]byte, len(leaves))
	for i, l := range leaves {
		h := sha256.New()
		h.Write([]byte{0})
		h.Write(l)
		copy(hashes[i][:], h.Sum(nil))
	}
	return merkle(hashes)
}

func merkle(h [][32]byte) [32]byte {
	if len(h) == 1 {
		return h[0]
	}
	k := 1
	for k*2 < len(h) {
		k *= 2
	}
	l, r := merkle(h[:k]), merkle(h[k:])
	d := sha256.New()
	d.Write([]byte{1})
	d.Write(l[:])
	d.Write(r[:])
	var out [32]byte
	copy(out[:], d.Sum(nil))
	return out
}
