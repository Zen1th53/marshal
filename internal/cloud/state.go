package cloud

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrState marks a failure to read or write local client state.
var ErrState = errors.New("cloud: state")

// State is what the client keeps between runs.
//
// Only the private key is genuinely sensitive. Everything else is a cache:
// deleting this file costs a registration round trip, not an entitlement, which
// is the property that makes the file safe to delete when something goes wrong.
type State struct {
	InstallationID string `json:"installation_id"`
	// PrivateKey is the installation key. Proof of possession rests on it and
	// it never leaves this machine, which is why the file holding it is 0600.
	PrivateKey []byte `json:"private_key"`
	// CachedEntitlement is a hint for what to display before the first lease
	// arrives. It is never an authority: the server re-decides entitlement on
	// every lease request regardless of what this says.
	CachedEntitlement string `json:"cached_entitlement,omitempty"`
}

// Public returns the installation's public key.
func (s State) Public() (ed25519.PublicKey, error) {
	if len(s.PrivateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: installation key is malformed", ErrState)
	}
	return ed25519.PrivateKey(s.PrivateKey).Public().(ed25519.PublicKey), nil
}

// Sign signs a challenge with the installation key, proving possession.
func (s State) Sign(message []byte) ([]byte, error) {
	if len(s.PrivateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: installation key is malformed", ErrState)
	}
	return ed25519.Sign(ed25519.PrivateKey(s.PrivateKey), message), nil
}

// Store persists client state inside the project's runtime directory.
type Store struct {
	mu   sync.Mutex
	path string
}

// NewStore roots client state in runtimeDir, alongside MARSHAL's other state.
func NewStore(runtimeDir string) *Store {
	return &Store{path: filepath.Join(runtimeDir, "cloud_state.json")}
}

// Path reports where state is kept, for diagnostics that must not print it.
func (s *Store) Path() string { return s.path }

// Load reads state, refusing a file other users on the machine can read.
//
// This mirrors how internal/auth treats its token file, and for the same
// reason: a key any local user can read has to be assumed compromised, so
// continuing quietly would mean proof of possession proving nothing about who
// is actually asking.
func (s *Store) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	info, err := os.Stat(s.path)
	if err != nil {
		return State{}, fmt.Errorf("%w: %s", ErrState, err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return State{}, fmt.Errorf(
			"%w: %s has insecure permissions %04o (expected 0600)", ErrState, s.path, perm)
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return State{}, fmt.Errorf("%w: %s", ErrState, err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("%w: %s", ErrState, err)
	}
	return st, nil
}

// Save writes state atomically with 0600 permissions.
//
// The write goes to a temporary file that is created 0600 from the start and
// then renamed. Writing in place would leave a window where the key sits in a
// half-written file, and creating then chmod-ing would leave a window where it
// is readable.
func (s *Store) Save(st State) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: %s", ErrState, err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("%w: %s", ErrState, err)
	}
	temporary, err := os.CreateTemp(dir, ".cloud_state-*")
	if err != nil {
		return fmt.Errorf("%w: %s", ErrState, err)
	}
	name := temporary.Name()
	defer os.Remove(name)

	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("%w: %s", ErrState, err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("%w: %s", ErrState, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("%w: %s", ErrState, err)
	}
	if err := os.Rename(name, s.path); err != nil {
		return fmt.Errorf("%w: %s", ErrState, err)
	}
	return nil
}

// NewInstallation mints a fresh installation identity.
//
// The identifier is random rather than derived from anything about the machine.
// Deriving it from a hostname, MAC address or project path would make it a
// fingerprint, and the telemetry rules forbid carrying that kind of information
// to the server at all.
func NewInstallation() (State, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return State{}, fmt.Errorf("%w: %s", ErrState, err)
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return State{}, fmt.Errorf("%w: %s", ErrState, err)
	}
	return State{
		InstallationID: "inst-" + hex.EncodeToString(raw),
		PrivateKey:     priv,
	}, nil
}

// NewSessionID mints an opaque per-session identifier.
//
// New on every run, so sessions cannot be linked across runs by anyone reading
// the server's records.
func NewSessionID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("%w: %s", ErrState, err)
	}
	return "sess-" + hex.EncodeToString(raw), nil
}

// LoadOrCreate returns existing state, or mints and persists a new identity.
func (s *Store) LoadOrCreate() (State, error) {
	st, err := s.Load()
	if err == nil {
		return st, nil
	}
	// A state file that exists but is unreadable or insecure is a problem to
	// report, not to paper over by minting a second identity beside it.
	if !errors.Is(err, ErrState) || fileExists(s.path) {
		return State{}, err
	}
	fresh, err := NewInstallation()
	if err != nil {
		return State{}, err
	}
	if err := s.Save(fresh); err != nil {
		return State{}, err
	}
	return fresh, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
