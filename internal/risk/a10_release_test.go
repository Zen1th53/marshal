package risk

import (
	"context"
	"testing"
)

func TestA10ProviderMetadataCannotChangeRiskSemantics(t *testing.T) {
	providers := []string{"Codex", "Claude", "Gemini", "OpenCode"}
	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			store := &memoryAssessmentStore{}
			assessment, err := NewEngine(store).Assess(context.Background(), AssessmentRequest{
				ID: AssessmentID("a10-" + provider),
				Descriptor: ToolDescriptor{
					Tool:     provider,
					Action:   "filesystem.write",
					Resource: "repo:marshal",
					Factors:  Factors{ExternalWrite: true, ScopeBreadth: 1},
				},
			})
			if err != nil {
				t.Fatalf("Assess(%q): %v", provider, err)
			}
			if assessment.Level != LevelHigh || assessment.Score != 4 {
				t.Fatalf("provider %q changed risk semantics: level=%s score=%d", provider, assessment.Level, assessment.Score)
			}
			if len(assessment.RequiredCapabilities) == 0 {
				t.Fatalf("provider %q did not receive the canonical high-risk capability requirement", provider)
			}
		})
	}
}
