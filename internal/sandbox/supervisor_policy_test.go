package sandbox

import (
	"encoding/binary"
	"net"
	"slices"
	"testing"
)

func inet4(ip string, port uint16) []byte {
	b := make([]byte, 16)
	binary.LittleEndian.PutUint16(b, afInetFamily)
	binary.BigEndian.PutUint16(b[2:], port)
	copy(b[4:8], net.ParseIP(ip).To4())
	return b
}

func inet6(ip string, port uint16) []byte {
	b := make([]byte, 28)
	binary.LittleEndian.PutUint16(b, afInet6Family)
	binary.BigEndian.PutUint16(b[2:], port)
	copy(b[8:24], net.ParseIP(ip).To16())
	return b
}

const (
	afUnixFamily  = 1
	afInetFamily  = 2
	afInet6Family = 10
)

// Extreme reaches the worker's own loopback (IPv4 and IPv6) and Unix sockets
// and keeps localhost out of the proxy; an outward address stays refused.
// Community allows none of it and proxies every name.
func TestSupervisorLocalPolicyFollowsTheEdition(t *testing.T) {
	unixPath := func(p string) []byte { return append([]byte{afUnixFamily, 0}, append([]byte(p), 0)...) }
	local := [][]byte{inet4("127.0.0.1", 3000), inet4("127.0.0.53", 53), inet6("::1", 8080), {afUnixFamily, 0, 0, 'x'}}
	for _, addr := range local {
		if buildSupervisorAllowsConnect(addr) != editionIsExtreme {
			t.Fatalf("local connect %v allowed=%t", addr, !editionIsExtreme)
		}
	}
	// A host-shared Unix path could be a relay around the proxy.
	for _, addr := range [][]byte{inet4("192.0.2.1", 443), inet6("2001:db8::1", 443), {}, {afInetFamily}, unixPath("/work/tree/relay.sock"), unixPath("/tmp/dev.sock"), unixPath("/run/marshal-egress/proxy.sock")} {
		if buildSupervisorAllowsConnect(addr) {
			t.Fatalf("outward connect %v allowed", addr)
		}
	}
	for _, family := range []uint64{1, 2, 10, 16} {
		if buildSupervisorAllowsSocket(family) != editionIsExtreme {
			t.Fatalf("socket family %d", family)
		}
	}
	if buildSupervisorAllowsSocket(17) {
		t.Fatal("packet sockets allowed")
	}
	env := proxyEnvironment()
	want := "NO_PROXY="
	if editionIsExtreme {
		want = "NO_PROXY=localhost,127.0.0.1,::1"
	}
	if !slices.Contains(env, want) {
		t.Fatalf("proxy environment %v", env)
	}
}
