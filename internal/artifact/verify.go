package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

// Payload states reported by Verify. Only PayloadVerified means the stored
// bytes were read and match the registered digest; every other state is a
// reason the record must not be treated as verified evidence.
const (
	PayloadVerified     = "VERIFIED"
	PayloadMissing      = "MISSING"
	PayloadMismatch     = "DIGEST_MISMATCH"
	PayloadOutsideStore = "OUTSIDE_STORE"
	PayloadTooLarge     = "NOT_CHECKED_TOO_LARGE"
	PayloadUnreadable   = "UNREADABLE"
)

// MaxVerifyBytes bounds how much an interactive check will hash.
const MaxVerifyBytes = 256 << 20

// Verify re-reads an artifact's stored bytes and compares them with its
// registered digest. It never trusts the registered path blindly: the payload
// must be a regular file inside the content-addressed store under root.
func Verify(root string, a model.Artifact) (string, error) {
	digestRoot, err := filepath.Abs(filepath.Join(root, "sha256"))
	if err != nil {
		return PayloadUnreadable, err
	}
	path, err := filepath.Abs(a.Path)
	if err != nil || a.Path == "" || !strings.HasPrefix(path, digestRoot+string(filepath.Separator)) {
		return PayloadOutsideStore, nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return PayloadMissing, nil
	}
	if err != nil {
		return PayloadUnreadable, err
	}
	if !info.Mode().IsRegular() {
		return PayloadOutsideStore, nil
	}
	if info.Size() > MaxVerifyBytes {
		return PayloadTooLarge, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return PayloadUnreadable, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, MaxVerifyBytes+1)); err != nil {
		return PayloadUnreadable, fmt.Errorf("hash artifact: %w", err)
	}
	if "sha256:"+hex.EncodeToString(hash.Sum(nil)) != a.Digest {
		return PayloadMismatch, nil
	}
	return PayloadVerified, nil
}
