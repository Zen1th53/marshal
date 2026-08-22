package webcontrol_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestResourcesEndpointRequiresAuthenticationAndNoRuntimeDoesNotFabricateData(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatal(err)
	}
	unauthorized := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/resources", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.Code)
	}
}
