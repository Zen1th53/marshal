//go:build !governed && linux

package netpolicy

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Each refusal names its own actionable cause, still wraps
// ErrCredentialBroker, and never carries the stored secret.
func TestAGYNativeCredentialFailuresNameDistinctCauses(t *testing.T) {
	good := agySecretItem{Path: "/synthetic/item", Attributes: agyNativeAttributes()}
	cases := map[string]struct {
		mutate func(*agyFixtureStore)
		want   string
	}{
		"missing":       {func(s *agyFixtureStore) { s.items = nil }, "no Antigravity sign-in in the keyring"},
		"locked":        {func(s *agyFixtureStore) { s.items[0].Locked = true }, "is locked; unlock it"},
		"duplicate":     {func(s *agyFixtureStore) { s.items = append(s.items, good) }, "2 keyring entries match"},
		"wrong-attrs":   {func(s *agyFixtureStore) { s.items[0].Attributes = map[string]string{"service": "other"} }, "attributes other than exactly"},
		"expired":       {func(s *agyFixtureStore) { s.raw = agyFixtureRaw(time.Now().Add(-time.Hour)) }, "access token expired at"},
		"expiring":      {func(s *agyFixtureStore) { s.raw = agyFixtureRaw(time.Now().Add(time.Minute)) }, "expires within two minutes"},
		"api-key-login": {func(s *agyFixtureStore) { s.raw = []byte(strings.Replace(string(s.raw), `"consumer"`, `"api_key"`, 1)) }, "not a consumer (Google account) sign-in"},
		"flat-format":   {func(s *agyFixtureStore) { s.raw = []byte(`{"access_token":"synthetic-host-access"}`) }, "not in the Antigravity consumer sign-in format"},
		"empty":         {func(s *agyFixtureStore) { s.raw = nil }, "keyring entry is empty"},
	}
	seen := map[string]string{}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &agyFixtureStore{items: []agySecretItem{good}, raw: agyFixtureRaw(time.Now().Add(time.Hour))}
			tc.mutate(store)
			_, err := readAGYNativeCredential(t.Context(), store)
			if err == nil || !errors.Is(err, ErrCredentialBroker) {
				t.Fatalf("refusal lost its broker class: %v", err)
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("cause %q not named: %s", tc.want, msg)
			}
			for _, secret := range []string{"synthetic-host-access", "synthetic-host-refresh", "synthetic-host-id", "/synthetic/item"} {
				if strings.Contains(msg, secret) {
					t.Fatalf("message leaks %q: %s", secret, msg)
				}
			}
			if other, dup := seen[msg]; dup {
				t.Fatalf("%s and %s share one message", name, other)
			}
			seen[msg] = name
		})
	}
}
