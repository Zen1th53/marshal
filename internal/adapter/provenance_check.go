package adapter

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/provenance"
)

type ProvenanceChecker struct {
	engine *provenance.Engine
}

func NewProvenanceChecker(engine *provenance.Engine) *ProvenanceChecker {
	return &ProvenanceChecker{engine: engine}
}

func (c *ProvenanceChecker) VerifyCustody(ctx context.Context, changeID string) (*provenance.ChainCustodyView, error) {
	if c == nil || c.engine == nil {
		return nil, fmt.Errorf("provenance checker uninitialized")
	}
	return c.engine.Trace(ctx, changeID)
}
