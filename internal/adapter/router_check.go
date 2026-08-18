package adapter

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/router"
)

type MultiModelRouterService struct {
	router *router.Router
}

func NewMultiModelRouterService(router *router.Router) *MultiModelRouterService {
	return &MultiModelRouterService{router: router}
}

func (s *MultiModelRouterService) SelectModel(ctx context.Context, caps []string, minContext int) (*router.RouteDecision, error) {
	if s == nil || s.router == nil {
		return nil, fmt.Errorf("multi-model router service uninitialized")
	}
	return s.router.Route(ctx, caps, minContext)
}
