package cloud

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Identifiers must be opaque. Anything derived from a hostname, path or user
// name would be a fingerprint, and the telemetry rules forbid sending that.
var identifierPattern = regexp.MustCompile(`^(?:inst|sess)-[0-9a-f]{32}$`)

func TestNewInstallationIsOpaqueAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		st, err := NewInstallation()
		if err != nil {
			t.Fatalf("new installation: %v", err)
		}
		if !identifierPattern.MatchString(st.InstallationID) {
			t.Fatalf("installation id %q is not an opaque identifier", st.InstallationID)
		}
		if seen[st.InstallationID] {
			t.Fatalf("installation id %q was generated twice", st.InstallationID)
		}
		seen[st.InstallationID] = true

		// The identifier must not embed anything about this machine.
		for _, leak := range machineIdentifiers(t) {
			if leak != "" && strings.Contains(st.InstallationID, leak) {
				t.Fatalf("installation id leaks %q", leak)
			}
		}
	}
}

func TestNewSessionIDIsOpaqueAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		id, err := NewSessionID()
		if err != nil {
			t.Fatalf("new session: %v", err)
		}
		if !identifierPattern.MatchString(id) {
			t.Fatalf("session id %q is not opaque", id)
		}
		if seen[id] {
			t.Fatalf("session id %q was generated twice", id)
		}
		seen[id] = true
	}
}

func machineIdentifiers(t *testing.T) []string {
	t.Helper()
	host, _ := os.Hostname()
	wd, _ := os.Getwd()
	return []string{host, filepath.Base(wd), os.Getenv("USER"), os.Getenv("HOME")}
}

// The installation key is what proof of possession rests on, so state readable
// by other users on the machine has to be treated as compromised.
func TestStoreRefusesInsecurePermissions(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	fresh, err := NewInstallation()
	if err != nil {
		t.Fatalf("new installation: %v", err)
	}
	if err := s.Save(fresh); err != nil {
		t.Fatalf("save: %v", err)
	}

	info, err := os.Stat(s.Path())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("state written with mode %04o, want 0600", perm)
	}
	if _, err := s.Load(); err != nil {
		t.Fatalf("load of secure state failed: %v", err)
	}

	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o666} {
		if err := os.Chmod(s.Path(), mode); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		if _, err := s.Load(); !errors.Is(err, ErrState) {
			t.Fatalf("state with mode %04o was accepted", mode)
		}
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())
	fresh, err := NewInstallation()
	if err != nil {
		t.Fatalf("new installation: %v", err)
	}
	if err := s.Save(fresh); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.InstallationID != fresh.InstallationID {
		t.Fatal("installation id did not survive the round trip")
	}
	if string(loaded.PrivateKey) != string(fresh.PrivateKey) {
		t.Fatal("private key did not survive the round trip")
	}
}

// LoadOrCreate mints an identity when there is none, and reuses it thereafter.
// Minting a new one on every run would make an installation count meaningless.
func TestLoadOrCreateIsStable(t *testing.T) {
	s := NewStore(t.TempDir())
	first, err := s.LoadOrCreate()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := s.LoadOrCreate()
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.InstallationID != second.InstallationID {
		t.Fatal("LoadOrCreate minted a second identity instead of reusing the first")
	}
}

// A state file that exists but cannot be trusted is an error to report, not a
// reason to quietly mint a second identity beside it.
func TestLoadOrCreateRefusesInsecureExistingState(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if _, err := s.LoadOrCreate(); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := os.Chmod(s.Path(), 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if _, err := s.LoadOrCreate(); !errors.Is(err, ErrState) {
		t.Fatalf("insecure existing state was replaced rather than reported: %v", err)
	}
}

// Signing must fail loudly on malformed key material rather than producing a
// signature that will mysteriously not verify.
func TestSignRejectsMalformedKey(t *testing.T) {
	st := State{InstallationID: "inst-1", PrivateKey: []byte("too short")}
	if _, err := st.Sign([]byte("challenge")); !errors.Is(err, ErrState) {
		t.Fatalf("want ErrState, got %v", err)
	}
	if _, err := st.Public(); !errors.Is(err, ErrState) {
		t.Fatalf("want ErrState from Public, got %v", err)
	}
}

// A saved state file must not contain anything identifying the machine.
func TestSavedStateCarriesNoMachineIdentifiers(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if _, err := s.LoadOrCreate(); err != nil {
		t.Fatalf("create: %v", err)
	}
	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, leak := range machineIdentifiers(t) {
		// Short or empty values would match by coincidence and say nothing.
		if len(leak) < 4 {
			continue
		}
		if strings.Contains(string(raw), leak) {
			t.Fatalf("state file contains %q", leak)
		}
	}
}
