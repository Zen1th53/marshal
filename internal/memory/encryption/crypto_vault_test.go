package encryption_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/encryption"
)

func TestT151ProtectedMemoryEncryptionAtRest(t *testing.T) {
	ctx := context.Background()
	keyV1 := []byte("01234567890123456789012345678901") // 32-byte AES-256 key
	vault := encryption.NewVault(1, keyV1)

	plaintext := []byte("CONFIDENTIAL_SYSTEM_ARCHITECTURE_SECURITY_FINDING")
	memID := "MEM-SEC-01"
	rev := 1
	scopeID := "scope-tenant-alpha"

	// 1. Encrypt plaintext with AAD binding
	encPayload, err := vault.Encrypt(ctx, memID, rev, scopeID, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// 2. Decrypt roundtrip
	decrypted, err := vault.Decrypt(ctx, memID, rev, scopeID, encPayload)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Fatalf("expected plaintext %s, got: %s", string(plaintext), string(decrypted))
	}

	// 3. Ciphertext tamper detection
	tamperedBytes := make([]byte, len(encPayload.Ciphertext))
	copy(tamperedBytes, encPayload.Ciphertext)
	tamperedBytes[0] ^= 0xFF
	tampered := encPayload
	tampered.Ciphertext = tamperedBytes
	_, err = vault.Decrypt(ctx, memID, rev, scopeID, tampered)
	if !errors.Is(err, encryption.ErrDecryptionFailed) {
		t.Fatalf("expected ErrDecryptionFailed for tampered ciphertext, got: %v", err)
	}

	// 4. Wrong AAD (e.g. wrong scope) detection
	_, err = vault.Decrypt(ctx, memID, rev, "scope-unauthorized-tenant", encPayload)
	if !errors.Is(err, encryption.ErrDecryptionFailed) {
		t.Fatalf("expected ErrDecryptionFailed for mismatched AAD scope, got: %v", err)
	}

	// 5. Key rotation (Epoch 1 -> Epoch 2)
	keyV2 := []byte("98765432109876543210987654321098")
	vault.RotateKey(2, keyV2)

	reEncrypted, err := vault.ReEncrypt(ctx, memID, rev, scopeID, encPayload)
	if err != nil {
		t.Fatalf("ReEncrypt: %v", err)
	}
	if reEncrypted.KeyEpoch != 2 {
		t.Fatalf("expected key epoch 2 after rotation, got: %d", reEncrypted.KeyEpoch)
	}

	decryptedV2, err := vault.Decrypt(ctx, memID, rev, scopeID, reEncrypted)
	if err != nil {
		t.Fatalf("Decrypt after rotation: %v", err)
	}
	if string(decryptedV2) != string(plaintext) {
		t.Fatalf("expected decrypted plaintext to match after rotation")
	}
}
