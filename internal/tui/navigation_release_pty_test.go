//go:build linux

package tui

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/cloud"
)

// Item 1's real-binary PTY qualification.
//
// The in-process tests in navigation_release_test.go exercise the gate with a
// workspace built by the test; TestMain's navigationReleased=true assignment
// does not reach a separately compiled binary, so these tests build the real
// `marshal` binary and drive it over an actual pseudo-terminal. The entitled
// tests run the binary against a loopback Community Cloud fixture that mints
// genuinely signed leases, so entitlement travels the production authorization
// path (key discovery, registration, proof-of-possession challenge, signed
// lease) instead of a test-only bypass.

// localCloudFixture is a loopback Community Cloud that mints real, signed ULTRA
// leases. Plain HTTP is accepted because the client permits exactly one
// non-TLS case — loopback — so the whole authorization path can be exercised
// without a certificate. The lease signature is verified by cloud.Gate.Adopt
// exactly as a server-issued lease would be.
type localCloudFixture struct {
	keyID string
	priv  ed25519.PrivateKey
	srv   *httptest.Server

	mu            sync.Mutex
	leaseLifetime time.Duration
	refuseLease   bool
}

func newLocalCloudFixture(t *testing.T) *localCloudFixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate fixture signing key: %v", err)
	}
	f := &localCloudFixture{keyID: "k-nav-lease", priv: priv, leaseLifetime: 5 * time.Minute}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/keys", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kid":        f.keyID,
				"algorithm":  "ed25519",
				"public_key": base64.RawURLEncoding.EncodeToString(pub),
			}},
		})
	})
	mux.HandleFunc("/v1/installations/register", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v1/ultra/challenge", func(w http.ResponseWriter, _ *http.Request) {
		now := time.Now().UTC()
		json.NewEncoder(w).Encode(map[string]any{
			"nonce": "nonce-nav", "issued_at": now, "expires_at": now.Add(time.Minute),
		})
	})
	mux.HandleFunc("/v1/ultra/sessions", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		refuse := f.refuseLease
		lifetime := f.leaseLifetime
		f.mu.Unlock()
		if refuse {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{
				"code": "entitlement_unavailable", "message": "revoked",
			})
			return
		}
		var body struct {
			InstallationID string `json:"installation_id"`
			SessionID      string `json:"session_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		now := time.Now().UTC()
		json.NewEncoder(w).Encode(mintCloudLease(f.priv, f.keyID, body.InstallationID, body.SessionID, now, lifetime))
	})
	mux.HandleFunc("/v1/ultra/heartbeat", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v1/telemetry/events", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"accepted": 0, "submitted": 0})
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *localCloudFixture) URL() string { return f.srv.URL }

func (f *localCloudFixture) setLeaseLifetime(d time.Duration) {
	f.mu.Lock()
	f.leaseLifetime = d
	f.mu.Unlock()
}

func (f *localCloudFixture) refuseLeases(refuse bool) {
	f.mu.Lock()
	f.refuseLease = refuse
	f.mu.Unlock()
}

// mintCloudLease signs a lease exactly as the server does: claims and bundle
// marshalled together, ed25519-signed with the key the client fetched from
// /v1/keys. cloud.Gate.Adopt verifies this against the trusted key ring.
func mintCloudLease(priv ed25519.PrivateKey, keyID, inst, sess string, issued time.Time, lifetime time.Duration) cloud.Lease {
	claims := cloud.Claims{
		JTI:            fmt.Sprintf("jti-nav-%d", issued.UnixNano()),
		KeyID:          keyID,
		EntitlementID:  "ent-nav",
		InstallationID: inst,
		SessionID:      sess,
		Capabilities:   []string{cloud.CapabilityDelegation},
		IssuedAt:       issued,
		ExpiresAt:      issued.Add(lifetime),
	}
	bundle := cloud.Bundle{
		PolicyDigest: "policy-nav",
		RoutingTable: map[string]string{"default": "route-nav"},
		IssuedFor:    inst,
	}
	input, _ := json.Marshal(struct {
		Claims cloud.Claims `json:"claims"`
		Bundle cloud.Bundle `json:"bundle"`
	}{claims, bundle})
	return cloud.Lease{
		Claims:    claims,
		Bundle:    bundle,
		Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, input)),
	}
}

// envOverrides builds a child environment from os.Environ() with the named keys
// replaced. Appending to os.Environ() would leave duplicates, and getenv
// returns the first match, so this removes the overridden keys first.
func envOverrides(overrides map[string]string) []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if _, replaced := overrides[k]; replaced {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

// startTUIInProjectEnv runs the compiled binary on a PTY with a custom
// environment. It mirrors startFrozenTUIInProject but allows the Cloud endpoint
// and machine-state directory to be pointed at the fixture, and the rest of the
// environment to be inherited.
func startTUIInProjectEnv(t *testing.T, rows, cols uint16, bin, project string, overrides map[string]string, args ...string) *ptySession {
	t.Helper()

	master, slave := openPTY(t)
	setWinsize(master, rows, cols)

	cmd := exec.Command(bin, args...)
	cmd.Dir = project
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	cmd.Env = envOverrides(overrides)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start marshal tui on pty: %v", err)
	}
	slave.Close()

	s := &ptySession{t: t, master: master, cmd: cmd}
	go func() {
		chunk := make([]byte, 8192)
		for {
			n, err := master.Read(chunk)
			if n > 0 {
				s.mu.Lock()
				s.buf.Write(chunk[:n])
				s.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	s.waitFor("Marshal", 15*time.Second)
	return s
}

// startCloudTUI starts the compiled binary pointed at the fixture's Cloud, with
// the machine-level installation identity isolated to a scratch XDG_CONFIG_HOME
// so the test never touches the operator's real ~/.config/marshal.
func startCloudTUI(t *testing.T, rows, cols uint16, endpoint string) *ptySession {
	t.Helper()
	bin := buildMarshalBinary(t)
	project := initProject(t, bin)
	return startTUIInProjectEnv(t, rows, cols, bin, project, map[string]string{
		"MARSHAL_CLOUD_ENDPOINT":  endpoint,
		"MARSHAL_NO_UPDATE_CHECK": "1",
		"XDG_CONFIG_HOME":         t.TempDir(),
	}, "tui")
}

// TestPTYNavigationLocalCloudAuthorizes is the harness proof: a real `marshal
// tui` reaches the loopback Cloud and ends up entitled through the production
// authorization path. It does not depend on the navigation release switch, so
// it runs (and must pass) under both editions.
func TestPTYNavigationLocalCloudAuthorizes(t *testing.T) {
	f := newLocalCloudFixture(t)
	s := startCloudTUI(t, 40, 120, f.URL())
	s.mustSee("Marshal")

	if editionExtreme {
		// The Marshal build opens the Board, which has no command line, and
		// grants ULTRA locally without a cloud check; ultra_extreme_test.go
		// covers that status text.
		return
	}
	s.sendLine("/ultra")
	s.mustSee("ENTITLED")
}
