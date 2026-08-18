package adapter

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/router"
)

func TestMultiModelRouterServiceAdapter(t *testing.T) {
	rt := router.NewRouter()
	ctx := context.Background()
	svc := NewMultiModelRouterService(rt)

	dec, err := svc.SelectModel(ctx, []string{"code"}, 16000)
	if err != nil {
		t.Fatalf("SelectModel failed: %v", err)
	}
	if dec.Provider == "" {
		t.Fatal("expected valid provider")
	}
}
