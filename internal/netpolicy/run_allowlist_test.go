package netpolicy

import (
	"context"
	"strings"
	"testing"
)

func TestRunAllowlistExactEndpoints(t *testing.T) {
	a, err := NewRunAllowlist([]string{"api.example.com", "127.0.0.1:8080", "[::1]:8080"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host    string
		port    int
		allowed bool
	}{
		{"api.example.com", 443, true}, {"API.EXAMPLE.COM.", 443, true},
		{"api.example.com", 80, false}, {"other.api.example.com", 443, false},
		{"api.example.com.evil", 443, false}, {"example.com", 443, false},
		{"127.0.0.1", 8080, true}, {"localhost", 8080, false},
		{"127.0.0.1", 8081, false}, {"::1", 8080, true}, {"::1", 443, false},
	} {
		d, err := a.Evaluate(context.Background(), Request{Host: tc.host, Port: tc.port, Protocol: ProtocolTCP})
		if err != nil || d.Allowed != tc.allowed {
			t.Fatalf("%s:%d: %+v %v", tc.host, tc.port, d, err)
		}
	}
	if err := a.Set("new.example.com:8443", true, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	d, _ := a.Evaluate(context.Background(), Request{Host: "new.example.com", Port: 8443, Protocol: ProtocolTCP})
	if !d.Allowed {
		t.Fatal("grant did not apply")
	}
	if err := a.Set("new.example.com:8443", false, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	d, _ = a.Evaluate(context.Background(), Request{Host: "new.example.com", Port: 8443, Protocol: ProtocolTCP})
	if d.Allowed {
		t.Fatal("revoke did not apply")
	}
	other, _ := NewRunAllowlist(nil)
	d, _ = other.Evaluate(context.Background(), Request{Host: "api.example.com", Port: 443, Protocol: ProtocolTCP})
	if d.Allowed {
		t.Fatal("allowlist leaked between runs")
	}
}

func TestRunAllowlistRejectsPatternsAndText(t *testing.T) {
	for _, endpoint := range []string{"*.example.com", "https://api.example.com", "api.example.com:0", "api.example.com:65536", "api.example.com:*", "allow api.example.com", "/egress allow RUN-x api.example.com", "api.example.com\n", "127.1", "[fe80::1%lo]:443", "[example.com]", strings.Repeat("a", 64) + ".example.com", strings.Repeat("a.", 128) + "com"} {
		if _, err := NewRunAllowlist([]string{endpoint}); err == nil {
			t.Errorf("accepted %q", endpoint)
		}
	}
}
