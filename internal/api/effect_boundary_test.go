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
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
)

func TestVerifyRouteConfinesProjectCode(t *testing.T) {
	runtime, _, root := apiRuntimeWithRoot(t)
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
	if sandbox.HostExecutionPreferred() {
		var result app.VerifyResult
		if response.Code != http.StatusOK || envelope.Error != nil || json.Unmarshal(envelope.Data, &result) != nil || result.ExitStatus != 0 || result.Isolation == nil || result.Isolation.Level != model.IsolationProcessOnly || !strings.Contains(result.Isolation.Reason, "Marshal verifies on the host by design") {
			t.Fatalf("host verification not reported: %d %s", response.Code, response.Body.String())
		}
		if body, err := os.ReadFile(marker); err != nil || string(body) != "ran" {
			t.Fatalf("verification did not execute on host: %q %v", body, err)
		}
		return
	}
	if envelope.Error == nil || !((response.Code == http.StatusServiceUnavailable && envelope.Error.Code == "unavailable" && strings.Contains(envelope.Error.Message, "required isolation cannot be enforced")) || (response.Code == http.StatusInternalServerError && envelope.Error.Code == "internal" && strings.HasPrefix(envelope.Error.Message, "verification failed with exit status "))) {
		t.Fatalf("request did not reach verification boundary: %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("verification escaped: %v", err)
	}
}
