package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/update"
)

func TestCLIUpdateRequiresConfirmation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			t.Errorf("unexpected download: %s", r.URL.Path)
		}
		w.Write([]byte(`{"tag_name":"v9.9.9","schema_version":9999}`))
	}))
	defer server.Close()
	t.Setenv(update.APIEnv, server.URL)
	old := Version
	Version = "v1.0.0"
	defer func() { Version = old }()
	var out bytes.Buffer
	c := command{stdout: &out}
	err := c.update(context.Background(), []string{"install"})
	if err == nil || !strings.Contains(err.Error(), "from v1.0.0 → to v9.9.9") || !strings.Contains(err.Error(), "confirmation required") {
		t.Fatalf("got %v", err)
	}
}

func TestCLIDevUpdateNeverDownloads(t *testing.T) {
	old := Version
	Version = "dev"
	defer func() { Version = old }()
	t.Setenv(update.APIEnv, "http://127.0.0.1:1")
	var out bytes.Buffer
	err := (command{stdout: &out}).update(context.Background(), []string{"install", "--yes"})
	if err != nil || !strings.Contains(out.String(), "dev build") {
		t.Fatalf("%s: %v", out.String(), err)
	}
}
