package mcp

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

func TestVerificationToolConfinesProjectCode(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	marker := filepath.Join(t.TempDir(), "marker")
	if err := os.MkdirAll(filepath.Join(repo.Path(), "conformance"), 0755); err != nil {
		t.Fatal(err)
	}
	script := "from pathlib import Path\nPath('" + marker + "').write_text('ran')\n"
	if err := os.WriteFile(filepath.Join(repo.Path(), "conformance", "runner.py"), []byte(script), 0644); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"verification_status","arguments":{}}}`))
	NewServer(runtime).Handler().ServeHTTP(response, request)
	var envelope struct {
		ID    int `json:"id"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || envelope.ID != 1 || envelope.Error == nil || envelope.Error.Code != -32000 || !(strings.Contains(envelope.Error.Message, "required isolation cannot be enforced") || strings.HasPrefix(envelope.Error.Message, "verification failed with exit status ")) {
		t.Fatalf("request did not reach verification boundary: %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("verification escaped: %v", err)
	}
}
