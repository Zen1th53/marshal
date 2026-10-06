package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

func TestVerifyRouteConfinesProjectCode(t *testing.T) {
	runtime, socket := apiRuntime(t)
	root := filepath.Dir(filepath.Dir(socket))
	marker := filepath.Join(t.TempDir(), "marker")
	if err := os.MkdirAll(filepath.Join(root, "conformance"), 0755); err != nil {
		t.Fatal(err)
	}
	script := "from pathlib import Path\nPath('" + marker + "').write_text('ran')\n"
	if err := os.WriteFile(filepath.Join(root, "conformance", "runner.py"), []byte(script), 0644); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(app.VerifyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	NewServer(runtime).routes().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/verify", strings.NewReader(string(body))))
	var envelope Envelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error == nil || !((response.Code == http.StatusServiceUnavailable && envelope.Error.Code == "unavailable" && strings.Contains(envelope.Error.Message, "required isolation cannot be enforced")) || (response.Code == http.StatusInternalServerError && envelope.Error.Code == "internal" && strings.HasPrefix(envelope.Error.Message, "verification failed with exit status "))) {
		t.Fatalf("request did not reach verification boundary: %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("verification escaped: %v", err)
	}
}
