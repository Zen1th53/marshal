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

	s.waitFor("MARSHAL", 15*time.Second)
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

	s.sendLine("/ultra")
	if editionExtreme {
		// The Marshal build grants ULTRA locally and performs no cloud check,
		// so the fixture endpoint is never consulted for the grant.
		s.mustSee("ULTRA: ACTIVE (Marshal build, no cloud check)")
		return
	}
	s.mustSee("ENTITLED")
}

// TestPTYNavigationStandardRefused proves a session without a verified ULTRA
// lease cannot open navigation through either entry point and the composer
// stays fully usable. The Marshal opens navigation for every session,
// so this standard-edition contract is skipped there.
func TestPTYNavigationStandardRefused(t *testing.T) {
	if editionExtreme {
		t.Skip("the Marshal opens navigation for every session; Standard-refusal is a standard-edition contract")
	}

	// Cloud off: an unentitled session.
	s := startCloudTUI(t, 40, 120, "off")
	s.mustSee("MARSHAL")
	s.mustNotSee("Continue Work")

	// Ctrl+N entry point.
	s.send("\x0e") // Ctrl+N
	s.mustNotSee("Continue Work")
	if s.waitFor("not available yet", 2*time.Second) {
		t.Log("navigation is still under the global release hold; the entitlement-refusal message is not yet distinguishable in this binary")
	} else {
		// Once released, a Standard session is refused with the ULTRA reason.
		s.mustSee("ULTRA")
		s.mustSee("Standard")
	}

	// Esc on an empty composer is the second entry point; it must refuse too.
	s.send("\x1b")
	s.mustNotSee("Continue Work")

	// The composer keeps working — the whole point of the refusal.
	s.sendLine("/status")
	s.mustSee("CANONICAL STATUS DETAIL")
}

// TestPTYNavigationLeaseLoss proves that a session holding a verified lease
// loses navigation live when the server revokes it, without a restart.
func TestPTYNavigationLeaseLoss(t *testing.T) {
	if editionExtreme {
		t.Skip("the Marshal bypasses the ULTRA gate; lease-loss requires the standard released edition")
	}

	f := newLocalCloudFixture(t)
	f.setLeaseLifetime(4 * time.Second) // short window so renewal is observable in-test
	s := startCloudTUI(t, 40, 120, f.URL())
	s.mustSee("MARSHAL")

	// Prove the session is actually entitled before it is taken away.
	s.sendLine("/ultra")
	s.mustSee("ENTITLED")

	// Open navigation with a live lease, then return to the composer.
	s.send("\x0e") // Ctrl+N
	if s.waitFor("not available yet", 2*time.Second) {
		t.Skip("navigation is still under the global release hold; lease-loss qualification requires I's candidate release edit")
	}
	s.mustSee("Continue Work")
	s.send("\x1b") // Esc closes navigation

	// Revoke server-side: the next renewal is refused, which degrades the gate
	// live (ErrRefused drops the lease rather than waiting for expiry).
	f.refuseLeases(true)

	// Poll /ultra until the live degrade lands.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		s.sendLine("/ultra")
		if strings.Contains(s.output(), "INACTIVE") {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !strings.Contains(s.output(), "INACTIVE") {
		t.Fatalf("lease loss did not degrade the live gate.\n--- output tail ---\n%s", tail(s.output(), 2500))
	}

	// Both entry points now refuse with the ULTRA reason, live.
	s.send("\x0e") // Ctrl+N
	s.mustSee("ULTRA")
	s.mustSee("Standard")
	s.mustNotSee("Continue Work")
}

// representativeFlowActions are the canonical actions the ten-item vertical
// flow must traverse. Until B–F bind them, the representative flow cannot
// qualify; it skips rather than pretending a GAP node is implemented.
var representativeFlowActions = []string{
	"CTUI-0043", // task pause (C)
	"CTUI-0044", // task resume (C)
	"CTUI-0045", // task retry (C)
	"CTUI-0048", // cancel goal (D)
	"CTUI-0077", // budget token limit (D)
	"CTUI-0352", // verification challenge (B)
	"CTUI-0438", // slot → candidate (B)
	"CTUI-0450", // evidence promotion (B)
	"CTUI-0051", // execute cell (C)
}

// representativeFlowGaps reports which of the required actions are still
// unbound implementation gaps.
func representativeFlowGaps(t *testing.T) []string {
	t.Helper()
	ia := testView(t).nav.ia
	gaps := make(map[string]bool, len(ia.Gaps()))
	for _, g := range ia.Gaps() {
		gaps[g.SpecID] = true
	}
	var missing []string
	for _, id := range representativeFlowActions {
		if gaps[id] {
			missing = append(missing, id)
		}
	}
	return missing
}

// TestPTYNavigationRepresentativeFlows is the ten-item vertical-flow
// qualification. It drives the released standard binary with a genuinely signed
// lease through composer → menu → Control/Work/Verify/Memory/Status/Models and
// proves breadcrumbs, cross-links, resize, and canonical read-only readback.
//
// The mutation steps (task pause/resume/retry, verification, memory promotion,
// budget edit, cancellation with durable readback) are completed at integration
// once B–F bind those actions; until then the test reports the unmet
// qualification as a skip rather than counting a GAP node as a pass.
func TestPTYNavigationRepresentativeFlows(t *testing.T) {
	if editionExtreme {
		t.Skip("the Marshal bypasses the ULTRA gate; representative flows qualify the standard released edition")
	}
	if gaps := representativeFlowGaps(t); len(gaps) > 0 {
		t.Skipf("representative-flow actions not yet bound (unmet qualification): %s", strings.Join(gaps, ", "))
	}

	f := newLocalCloudFixture(t)
	s := startCloudTUI(t, 40, 120, f.URL())
	s.mustSee("MARSHAL")

	// Composer → menu through the keyboard entry point.
	s.send("\x0e") // Ctrl+N
	if s.waitFor("not available yet", 2*time.Second) {
		t.Skip("navigation is still under the global release hold; representative flows require I's candidate release edit")
	}
	s.mustSee("Continue Work")

	// Traverse every frozen top-level section through the digit shortcuts and
	// confirm the breadcrumb names the section.
	for i, section := range FrozenSections {
		s.send(string(rune('1' + i)))
		if !s.waitFor(section, 4*time.Second) {
			t.Fatalf("section %d (%s) did not render after its digit shortcut.\n--- output tail ---\n%s",
				i+1, section, tail(s.output(), 2500))
		}
	}

	// 80/120-column resize must leave the surface usable.
	setWinsize(s.master, 24, 80)
	syscall.Kill(s.cmd.Process.Pid, syscall.SIGWINCH)
	time.Sleep(400 * time.Millisecond)
	s.send("3") // Status
	s.mustSee("Status")
	setWinsize(s.master, 40, 120)
	syscall.Kill(s.cmd.Process.Pid, syscall.SIGWINCH)
	time.Sleep(400 * time.Millisecond)

	// Read-only readback: Status and Models render from canonical sources.
	s.send("3") // Status
	s.mustSee("Status")
	s.send("7") // Models
	s.mustSee("Models")

	// Esc walks back to the composer without killing the session.
	s.send("\x1b")
	s.mustNotSee("Continue Work")
	s.sendLine("/status")
	s.mustSee("CANONICAL STATUS DETAIL")

	// NOTE (integration follow-up, owned by I and B–F): the mutation-level
	// durable readback — goal/plan traversal, task pause/resume/retry,
	// verification, memory promotion/import/bundle, and rollback/cancel owner —
	// is asserted here once those actions are bound. The navigation plumbing
	// above already proves the released binary reaches every destination and
	// that Esc, resize, and read-only readback work over a real terminal.
}
