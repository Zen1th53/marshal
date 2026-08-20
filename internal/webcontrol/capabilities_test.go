package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT171CapabilitiesDiscovery(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/capabilities", nil)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var capsDTO webcontrol.CapabilitiesDTO
	if err := json.NewDecoder(w.Body).Decode(&capsDTO); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// Invariant 1: Check presence of core capabilities
	requiredCaps := []string{"cap:system:read", "cap:task:run", "cap:memory:write"}
	for _, capName := range requiredCaps {
		capStatus, ok := capsDTO.Capabilities[capName]
		if !ok {
			t.Fatalf("missing required capability %s", capName)
		}
		if capStatus.State != webcontrol.CapStateAvailable {
			t.Fatalf("expected %s to be AVAILABLE, got %s", capName, capStatus.State)
		}
	}

	// Invariant 2: Security scan - zero secrets in capabilities JSON
	bodyStr := w.Body.String()
	for _, forbidden := range []string{"password", "private_key", "secret", "bearer"} {
		if strings.Contains(strings.ToLower(bodyStr), forbidden) {
			t.Fatalf("capability response contains sensitive token: %s", forbidden)
		}
	}
}
