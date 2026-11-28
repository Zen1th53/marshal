package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/update"
)

func TestUpdateInstallRequiresConfirmation(t *testing.T) {
	if update.PrivateEdition() {
		t.Skip("public updater disabled")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			t.Errorf("unexpected download: %s", r.URL.Path)
		}
		w.Write([]byte(`{"tag_name":"v9.9.9"}`))
	}))
	defer server.Close()
	t.Setenv(update.APIEnv, server.URL)
	old := BuildVersion
	BuildVersion = "v1.0.0"
	defer func() { BuildVersion = old }()
	h := NewCommandHandler(&Workspace{})
	got, err := h.handleUpdate(context.Background(), []string{"install"})
	if err != nil || !strings.Contains(got, "from v1.0.0 → to v9.9.9") || !strings.Contains(got, "/update install confirm v9.9.9") {
		t.Fatalf("%s: %v", got, err)
	}
	got, err = h.handleUpdate(context.Background(), []string{"install", "confirm", "v9.9.9"})
	if err != nil || !strings.Contains(got, "--allow-downgrade") {
		t.Fatalf("%s: %v", got, err)
	}
}

func TestDevUpdateDoesNotContactFeed(t *testing.T) {
	old := BuildVersion
	BuildVersion = "dev"
	defer func() { BuildVersion = old }()
	t.Setenv(update.APIEnv, "http://127.0.0.1:1")
	got, err := NewCommandHandler(&Workspace{}).handleUpdate(context.Background(), []string{"install"})
	if update.PrivateEdition() {
		return
	}
	if err != nil || !strings.Contains(got, "dev build") {
		t.Fatalf("%s: %v", got, err)
	}
}
