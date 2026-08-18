package risk

import (
	"context"
	"errors"
	"testing"
)

type denyingRiskAuthority struct{}

func (denyingRiskAuthority) AuthorizeRisk(context.Context, Assessment) error {
	return ErrAuthorizationDenied
}

func TestRiskScoreNeverAuthorizesOperation(t *testing.T) {
	engine := NewAuthorizedEngine(&memoryAssessmentStore{}, denyingRiskAuthority{})
	assessment, err := engine.Assess(context.Background(), AssessmentRequest{
		ID: "assessment-a04",
		Descriptor: ToolDescriptor{
			Tool: "git", Action: "push", Resource: "repo:marshal",
			Factors: Factors{ExternalWrite: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Require(context.Background(), assessment); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("Require error = %v, want ErrAuthorizationDenied", err)
	}
}

func TestAuthorizedEngineFailsClosedWithoutAuthority(t *testing.T) {
	engine := NewAuthorizedEngine(&memoryAssessmentStore{}, nil)
	if err := engine.Require(context.Background(), Assessment{ID: "assessment-a04", Level: LevelHigh, State: StateRequirementsEmitted}); !errors.Is(err, ErrAuthorizationUnavailable) {
		t.Fatalf("Require error = %v, want ErrAuthorizationUnavailable", err)
	}
}
