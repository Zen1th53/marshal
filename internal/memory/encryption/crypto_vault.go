package encryption

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"
)

var (
	ErrDecryptionFailed     = errors.New("decryption failed: ciphertext or authenticated associated data (AAD) invalid")
	ErrMissingDecryptionKey = errors.New("missing decryption key for epoch")
)

type EncryptedPayload struct {
	KeyEpoch   int    `json:"key_epoch"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

type Vault struct {
	mu           sync.RWMutex
	currentEpoch int
	keys         map[int][]byte
}

func NewVault(epoch int, key []byte) *Vault {
	keys := make(map[int][]byte)
	keys[epoch] = key
	return &Vault{
		currentEpoch: epoch,
		keys:         keys,
	}
}

func (v *Vault) RotateKey(newEpoch int, newKey []byte) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.currentEpoch = newEpoch
	v.keys[newEpoch] = newKey
}

func computeAAD(memID string, rev int, scopeID string) []byte {
	return []byte(fmt.Sprintf("%s:%d:%s", memID, rev, scopeID))
}

// Encrypt performs AES-GCM-256 authenticated encryption with AAD bound to memory identity and scope.
func (v *Vault) Encrypt(ctx context.Context, memID string, rev int, scopeID string, plaintext []byte) (EncryptedPayload, error) {
	v.mu.RLock()
	epoch := v.currentEpoch
	key := v.keys[epoch]
	v.mu.RUnlock()

	block, err := aes.NewCipher(key)
	if err != nil {
		return EncryptedPayload{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return EncryptedPayload{}, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return EncryptedPayload{}, err
	}

	aad := computeAAD(memID, rev, scopeID)
	ciphertext := gcm.Seal(nil, nonce, plaintext, aad)

	return EncryptedPayload{
		KeyEpoch:   epoch,
		Nonce:      nonce,
		Ciphertext: ciphertext,
	}, nil
}

// Decrypt verifies AAD and decrypts ciphertext using the matching key epoch.
func (v *Vault) Decrypt(ctx context.Context, memID string, rev int, scopeID string, payload EncryptedPayload) ([]byte, error) {
	v.mu.RLock()
	key, ok := v.keys[payload.KeyEpoch]
	v.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: epoch %d", ErrMissingDecryptionKey, payload.KeyEpoch)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	aad := computeAAD(memID, rev, scopeID)
	plaintext, err := gcm.Open(nil, payload.Nonce, payload.Ciphertext, aad)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	return plaintext, nil
}

// ReEncrypt rotates ciphertext to the latest key epoch.
func (v *Vault) ReEncrypt(ctx context.Context, memID string, rev int, scopeID string, payload EncryptedPayload) (EncryptedPayload, error) {
	plaintext, err := v.Decrypt(ctx, memID, rev, scopeID, payload)
	if err != nil {
		return EncryptedPayload{}, err
	}
	return v.Encrypt(ctx, memID, rev, scopeID, plaintext)
}
