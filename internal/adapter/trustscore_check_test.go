package adapter

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/trustscore"
)

func TestEvidenceTrustScoreServiceAdapter(t *testing.T) {
	ev := trustscore.NewEvaluator()
	ctx := context.Background()
	svc := NewEvidenceTrustScoreService(ev)

	res, err := svc.ScoreChange(ctx, "sha256:c1")
	if err != nil {
		t.Fatalf("ScoreChange failed: %v", err)
	}
	if !res.Eligible {
		t.Fatalf("expected Eligible = true")
	}
}
