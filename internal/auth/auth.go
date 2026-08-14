package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type PrincipalKind string

const (
	KindLocalUser PrincipalKind = "local_user"
	KindMCPClient PrincipalKind = "mcp_client"
	KindA2AAgent  PrincipalKind = "a2a_agent"
)

type Principal struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Kind         PrincipalKind `json:"kind"`
	Capabilities []string      `json:"capabilities"`
}

type TokenRecord struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Kind         PrincipalKind `json:"kind"`
	Digest       string        `json:"digest"`
	CreatedAt    time.Time     `json:"created_at"`
	Revoked      bool          `json:"revoked"`
	Capabilities []string      `json:"capabilities"`
}

type Manager struct {
	mu   sync.Mutex
	path string
}

func NewManager(runtimeDir string) *Manager {
	return &Manager{path: filepath.Join(runtimeDir, "auth_tokens.json")}
}

func (m *Manager) CreateToken(name string, kind PrincipalKind, capabilities []string) (string, TokenRecord, error) {
	if name == "" {
		return "", TokenRecord{}, errors.New("token name is required")
	}
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		return "", TokenRecord{}, fmt.Errorf("generate token entropy: %w", err)
	}
	plaintext := "marshal_token_" + hex.EncodeToString(rawBytes)
	digest := hashToken(plaintext)

	idBytes := make([]byte, 8)
	_, _ = rand.Read(idBytes)
	id := "TOKEN-" + hex.EncodeToString(idBytes)

	record := TokenRecord{
		ID:           id,
		Name:         name,
		Kind:         kind,
		Digest:       digest,
		CreatedAt:    time.Now().UTC(),
		Revoked:      false,
		Capabilities: capabilities,
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	records, _ := m.loadUnlocked()
	records = append(records, record)
	if err := m.saveUnlocked(records); err != nil {
		return "", TokenRecord{}, err
	}
	return plaintext, record, nil
}

func (m *Manager) Authenticate(plaintext string) (Principal, error) {
	if plaintext == "" {
		return Principal{}, errors.New("missing token")
	}
	digest := hashToken(plaintext)

	m.mu.Lock()
	defer m.mu.Unlock()

	records, err := m.loadUnlocked()
	if err != nil {
		return Principal{}, err
	}
	for _, rec := range records {
		if !rec.Revoked && subtle.ConstantTimeCompare([]byte(rec.Digest), []byte(digest)) == 1 {
			return Principal{
				ID:           rec.ID,
				Name:         rec.Name,
				Kind:         rec.Kind,
				Capabilities: rec.Capabilities,
			}, nil
		}
	}
	return Principal{}, errors.New("invalid or revoked token")
}

func (m *Manager) ListTokens() ([]TokenRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadUnlocked()
}

func (m *Manager) RevokeToken(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	records, err := m.loadUnlocked()
	if err != nil {
		return err
	}
	found := false
	for i := range records {
		if records[i].ID == id {
			records[i].Revoked = true
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("token %s not found", id)
	}
	return m.saveUnlocked(records)
}

func (m *Manager) loadUnlocked() ([]TokenRecord, error) {
	data, err := os.ReadFile(m.path)
	if os.IsNotExist(err) {
		return []TokenRecord{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read auth tokens: %w", err)
	}
	var records []TokenRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("parse auth tokens: %w", err)
	}
	return records, nil
}

func (m *Manager) saveUnlocked(records []TokenRecord) error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.path, data, 0o600)
}

func hashToken(plaintext string) string {
	h := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(h[:])
}

type SecretResolver struct{}

func NewSecretResolver() *SecretResolver { return &SecretResolver{} }

func (r *SecretResolver) Resolve(ref string) (string, error) {
	if strings.HasPrefix(ref, "env:") {
		varName := strings.TrimPrefix(ref, "env:")
		val := os.Getenv(varName)
		if val == "" {
			return "", fmt.Errorf("environment variable %s is empty or unset", varName)
		}
		return val, nil
	}
	if strings.HasPrefix(ref, "file:") {
		path := strings.TrimPrefix(ref, "file:")
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("stat secret file %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("secret file %s is not a regular file", path)
		}
		if info.Mode().Perm()&0o004 != 0 {
			return "", fmt.Errorf("secret file %s is world-readable", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read secret file %s: %w", path, err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return ref, nil
}

func RedactSecrets(content []byte, secrets []string) []byte {
	result := content
	for _, secret := range secrets {
		if len(secret) > 0 {
			result = bytes.ReplaceAll(result, []byte(secret), []byte("[REDACTED]"))
		}
	}
	return result
}
