package netpolicy

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
)

// A granted host name that resolves to a private or loopback address (a LAN
// registry, a host-side dev service) is reached in Extreme and recorded like
// any destination; Community keeps the DNS-rebinding/SSRF refusal.
func TestPrivateResolutionFollowsTheEdition(t *testing.T) {
	allow, err := NewRunAllowlist([]string{"localhost:5432"})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "p.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	p, err := NewEgressProxy(ProxyConfig{Evaluator: allow, Listener: ln})
	if err != nil {
		t.Fatal(err)
	}
	ip, decision, err := p.evaluateAndResolve(context.Background(), "localhost", 5432, ProtocolTCP)
	if buildPrivateResolutionAllowed() {
		if err != nil || !decision.Allowed || ip == nil || !ip.IsLoopback() {
			t.Fatalf("Marshal refused a granted private resolution: %v %+v %v", ip, decision, err)
		}
		return
	}
	if !errors.Is(err, ErrPrivateIPBlocked) || decision.Allowed {
		t.Fatalf("Community private resolution guard changed: %+v %v", decision, err)
	}
}
