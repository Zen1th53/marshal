package sandbox

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/netpolicy"
)

func TestNetworkEnvelopeFailsClosed(t *testing.T) {
	b := NewBwrap("/usr/bin/bwrap")
	if _, err := b.Wrap(model.SandboxRequest{Worktree: t.TempDir(), NetworkAllowed: true}, []string{"/bin/true"}); err == nil {
		t.Fatal("network enabled without bridge")
	}
}

func TestBwrapRunEgress(t *testing.T) {
	binary, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap unavailable")
	}
	b := NewBwrap(binary)
	if p := b.Probe(context.Background()); !p.Available {
		t.Skip(p.Reason)
	}
	socat, err := TrustedBridgePath()
	if err != nil {
		t.Skip(err)
	}
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("curl unavailable")
	}
	curl, err = filepath.EvalSymlinks(curl)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("allowed-server")) }))
	defer server.Close()
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	a, _ := netpolicy.NewRunAllowlist([]string{net.JoinHostPort(host, port)})
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "proxy.sock"))
	if err != nil {
		t.Fatal(err)
	}
	refused := make(chan string, 10)
	p, err := netpolicy.NewEgressProxy(netpolicy.ProxyConfig{Evaluator: a, Listener: ln, Attempt: func(_ context.Context, h string, port int, d netpolicy.Decision) error {
		if !d.Allowed {
			refused <- net.JoinHostPort(h, strconv.Itoa(port))
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	p.Start()
	defer p.Close()
	run := func(argv ...string) (string, error) {
		spec, err := b.Wrap(model.SandboxRequest{Worktree: t.TempDir(), NetworkAllowed: true, EgressSocket: ln.Addr().String(), BridgeBinary: socat}, argv)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(spec.Args, "--unshare-net") {
			t.Fatal("network namespace shared")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
		cmd.Env = spec.Env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run(curl, "--fail", "--max-time", "3", "--noproxy", "", "--proxy", SandboxProxyURL, server.URL)
	if err != nil || !strings.Contains(out, "allowed-server") {
		t.Fatalf("allowed: %v %s", err, out)
	}
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("connect-server")) }))
	defer tlsServer.Close()
	tlsEndpoint := strings.TrimPrefix(tlsServer.URL, "https://")
	if err := a.Set(tlsEndpoint, true, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if out, err := run(curl, "--fail", "--insecure", "--max-time", "3", "--noproxy", "", "--proxy", SandboxProxyURL, tlsServer.URL); err != nil || !strings.Contains(out, "connect-server") {
		t.Fatalf("CONNECT: %v %s", err, out)
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("denied destination reached") }))
	defer other.Close()
	if out, err := run(curl, "--fail", "--max-time", "3", "--noproxy", "", "--proxy", SandboxProxyURL, other.URL); err == nil {
		t.Fatalf("denied succeeded: %s", out)
	}
	select {
	case <-refused:
	case <-time.After(time.Second):
		t.Fatal("no refusal alert")
	}
	if out, err := run(curl, "--fail", "--max-time", "2", "--noproxy", "*", server.URL); err == nil {
		t.Fatalf("direct succeeded: %s", out)
	}
}

func TestEgressBridgeUsesResolvedPrivateMount(t *testing.T) {
	bridge, err := TrustedBridgePath()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(bridge)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "proxy.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	args, argv, err := egressEnvelope(ln.Addr().String(), bridge, []string{"/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	const target = "/run/marshal-egress/socat"
	if !slices.Contains(argv, target) {
		t.Fatalf("bridge executable not private: %v", argv)
	}
	found := false
	for i := 0; i+2 < len(args); i++ {
		if args[i] == "--ro-bind" && args[i+1] == resolved && args[i+2] == target {
			found = true
		}
	}
	if !found {
		t.Fatalf("resolved private bridge mount absent: %v", args)
	}
}
