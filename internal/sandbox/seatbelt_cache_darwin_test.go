//go:build darwin

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestSeatbeltProbeCacheReuseAndShape(t *testing.T) {
	resetSeatbeltProbeCache()
	defer resetSeatbeltProbeCache()

	backend := realSeatbelt(t)

	// First probe runs the full live probe.
	start := time.Now()
	capNetFalse := backend.ProbeRequest(context.Background(), model.SandboxRequest{NetworkAllowed: false})
	dur1 := time.Since(start)
	if !capNetFalse.Available || capNetFalse.Network {
		t.Fatalf("first probe unexpected: %#v", capNetFalse)
	}
	if dur1 < 200*time.Millisecond {
		t.Fatalf("expected first probe to run live probe (>200ms), took %v", dur1)
	}

	// Second probe with identical binary and policy shape must be a fast cache hit.
	start = time.Now()
	capNetFalse2 := backend.ProbeRequest(context.Background(), model.SandboxRequest{NetworkAllowed: false})
	dur2 := time.Since(start)
	if !capNetFalse2.Available || capNetFalse2.Network {
		t.Fatalf("cached probe unexpected: %#v", capNetFalse2)
	}
	if dur2 > 50*time.Millisecond {
		t.Fatalf("expected cache hit to be fast (<50ms), took %v", dur2)
	}

	// Third probe with different policy shape (NetworkAllowed=true) must NOT reuse net=false cache.
	start = time.Now()
	capNetTrue := backend.ProbeRequest(context.Background(), model.SandboxRequest{NetworkAllowed: true})
	dur3 := time.Since(start)
	if !capNetTrue.Available || !capNetTrue.Network {
		t.Fatalf("net=true probe unexpected: %#v", capNetTrue)
	}
	if dur3 < 200*time.Millisecond {
		t.Fatalf("expected net=true probe to run live probe (>200ms), took %v", dur3)
	}

	// Subsequent net=true probe must hit cache.
	start = time.Now()
	capNetTrue2 := backend.ProbeRequest(context.Background(), model.SandboxRequest{NetworkAllowed: true})
	dur4 := time.Since(start)
	if !capNetTrue2.Available || !capNetTrue2.Network {
		t.Fatalf("cached net=true probe unexpected: %#v", capNetTrue2)
	}
	if dur4 > 50*time.Millisecond {
		t.Fatalf("expected cached net=true hit to be fast (<50ms), took %v", dur4)
	}
}

func TestSeatbeltProbeCacheExpiry(t *testing.T) {
	resetSeatbeltProbeCache()
	defer resetSeatbeltProbeCache()

	// Set short TTL for test.
	restoreTTL := setSeatbeltProbeTTL(100 * time.Millisecond)
	defer restoreTTL()

	backend := realSeatbelt(t)

	// Cold probe.
	cap1 := backend.ProbeRequest(context.Background(), model.SandboxRequest{})
	if !cap1.Available {
		t.Fatalf("initial probe failed: %#v", cap1)
	}

	// Immediate hit.
	start := time.Now()
	cap2 := backend.ProbeRequest(context.Background(), model.SandboxRequest{})
	if !cap2.Available || time.Since(start) > 50*time.Millisecond {
		t.Fatalf("expected immediate cache hit, took %v", time.Since(start))
	}

	// Wait for TTL expiry.
	time.Sleep(150 * time.Millisecond)

	// After expiry, probe must re-run live verification.
	start = time.Now()
	cap3 := backend.ProbeRequest(context.Background(), model.SandboxRequest{})
	dur := time.Since(start)
	if !cap3.Available {
		t.Fatalf("re-probe after expiry failed: %#v", cap3)
	}
	if dur < 200*time.Millisecond {
		t.Fatalf("expected live re-probe after expiry (>200ms), took %v", dur)
	}
}

func TestSeatbeltProbeCacheBinaryIdentityInvalidation(t *testing.T) {
	resetSeatbeltProbeCache()
	defer resetSeatbeltProbeCache()

	dir := t.TempDir()
	binPath := filepath.Join(dir, "sandbox-exec")

	// Create a mock sandbox-exec script with strict 0700 permissions
	script := `#!/bin/sh
while [ $# -gt 0 ]; do
  if [ "$1" = "--" ]; then
    shift
    break
  fi
  shift
done
if echo "$*" | grep -q "seatbelt-denial-verified"; then
  printf seatbelt-denial-verified
  exit 0
fi
exit 0
`
	if err := os.WriteFile(binPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	backend := NewSeatbelt(binPath)

	// Initial probe with binary.
	cap1 := backend.ProbeRequest(context.Background(), model.SandboxRequest{})
	if !cap1.Available {
		t.Fatalf("probe failed with mock: %#v", cap1)
	}

	// Cache hit.
	start := time.Now()
	cap2 := backend.ProbeRequest(context.Background(), model.SandboxRequest{})
	if !cap2.Available || time.Since(start) > 50*time.Millisecond {
		t.Fatalf("expected cache hit, took %v", time.Since(start))
	}

	// Touch/modify binary modification time.
	newTime := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(binPath, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	// Modtime changed: cache must be invalidated and live probe re-run.
	cap3 := backend.ProbeRequest(context.Background(), model.SandboxRequest{})
	if !cap3.Available {
		t.Fatalf("probe after touch failed: %#v", cap3)
	}
}

func TestSeatbeltProbeCacheFailureNeverCachedAsSuccess(t *testing.T) {
	resetSeatbeltProbeCache()
	defer resetSeatbeltProbeCache()

	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "sandbox-exec")
	// Fake script that exits 0 but doesn't actually enforce denial
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	backend := NewSeatbelt(fakeBin)

	// First probe must fail.
	cap1 := backend.ProbeRequest(context.Background(), model.SandboxRequest{})
	if cap1.Available || !strings.Contains(cap1.Reason, "probe failed") {
		t.Fatalf("expected probe failure, got: %#v", cap1)
	}

	// Second probe must also fail; failure must NEVER be cached as success.
	cap2 := backend.ProbeRequest(context.Background(), model.SandboxRequest{})
	if cap2.Available || !strings.Contains(cap2.Reason, "probe failed") {
		t.Fatalf("expected second probe failure, got: %#v", cap2)
	}
}
