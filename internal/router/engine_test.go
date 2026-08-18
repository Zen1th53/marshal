package router

import (
	"context"
	"testing"
)

func TestRouterRoute(t *testing.T) {
	rt := NewRouter()
	ctx := context.Background()

	dec, err := rt.Route(ctx, []string{"code"}, 32000)
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if dec.Provider == "" || dec.Model == "" {
		t.Fatalf("expected valid route decision, got %+v", dec)
	}
}
