package cli

import "testing"

// --insecure is allowed on loopback only: an empty host binds every
// interface and must not count as loopback.
func TestIsLoopbackAddrRejectsAllInterfaces(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:8080": true,
		"localhost:8080": true,
		"[::1]:8080":     true,
		"127.0.0.2:8080": true,
		":8080":          false,
		"0.0.0.0:8080":   false,
		"[::]:8080":      false,
		"10.0.0.5:8080":  false,
		"":               false,
	} {
		if got := isLoopbackAddr(addr); got != want {
			t.Errorf("isLoopbackAddr(%q) = %v, want %v", addr, got, want)
		}
	}
}
