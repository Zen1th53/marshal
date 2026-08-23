package webcontrol_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestWebMemoryUsesCanonicalRuntimeStore(t *testing.T) {
	ctx := context.Background()
	repo := testgit.New(t)
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	now := time.Now().UTC()
	if err := rt.Store().WriteMemoryV2(ctx, model.MemoryRecordV2{
		ID: "MEM-LIVE-WEB", ProjectID: "PROJECT-local", Kind: model.MemoryKindDecision,
		Lifecycle: model.MemoryDurable, Confidence: model.ConfidenceVerified, Authority: model.AuthorityOperator,
		Title: "canonical web memory", Body: "served from memory_records_v2",
		Scope: string(model.ScopeProject), ScopeID: "PROJECT-local", Source: model.MemorySource{Kind: "test"},
		ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	srv, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, rt)
	if err != nil {
		t.Fatal(err)
	}
	cookie, _ := loginAndCSRF(t, srv, "operator", "admin")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory/search?query=canonical+web", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("search status=%d body=%s", w.Code, w.Body.String())
	}
	var response webcontrol.MemorySearchResponseDTO
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.TotalCount != 1 || response.Items[0].ID != "MEM-LIVE-WEB" || response.IndexStatus != "canonical" {
		t.Fatalf("web did not use canonical store: %+v", response)
	}
	for _, item := range response.Items {
		if item.ID == "MEM-001-ARCH-DECISION" {
			t.Fatal("dev fixture leaked into production server")
		}
	}
}
