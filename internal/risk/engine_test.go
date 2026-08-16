package risk

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

type memoryAssessmentStore struct {
	assessment Assessment
}

func (s *memoryAssessmentStore) PutRiskAssessment(_ context.Context, assessment Assessment) error {
	s.assessment = assessment
	return nil
}

func (s *memoryAssessmentStore) GetRiskAssessment(_ context.Context, _ AssessmentID) (Assessment, error) {
	if s.assessment.ID == "" {
		return Assessment{}, model.ErrNotFound
	}
	return s.assessment, nil
}

func (s *memoryAssessmentStore) TransitionRiskAssessmentState(_ context.Context, _ AssessmentID, _, _ AssessmentState) error {
	return nil
}

func TestEngineClassifiesAndEmitsRequirementsThroughExplicitStates(t *testing.T) {
	store := &memoryAssessmentStore{}
	engine := NewEngine(store)
	result, err := engine.Assess(context.Background(), AssessmentRequest{
		ID: "assessment-a03",
		Descriptor: ToolDescriptor{
			Tool:     "git",
			Action:   "push",
			Resource: "repo:marshal",
			Factors:  Factors{ExternalWrite: true},
		},
	})
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if result.Level != LevelHigh || result.State != StateRequirementsEmitted {
		t.Fatalf("assessment = %+v, want high/requirements_emitted", result)
	}
	if len(result.RequiredCapabilities) == 0 {
		t.Fatal("high-risk assessment emitted no capability requirement")
	}
}

func TestEngineRejectsIllegalStateTransition(t *testing.T) {
	if err := ValidateState(AssessmentState("finished")); err == nil {
		t.Fatal("unknown assessment state accepted")
	}
}

func TestEngineRejectsClaimedRiskDowngrade(t *testing.T) {
	engine := NewEngine(&memoryAssessmentStore{})
	_, err := engine.Assess(context.Background(), AssessmentRequest{
		ID: "assessment-a03-downgrade",
		Descriptor: ToolDescriptor{
			Tool: "git", Action: "push", Resource: "repo:marshal",
			Factors: Factors{ExternalWrite: true}, ClaimedLevel: LevelLow,
		},
	})
	if !errors.Is(err, ErrDowngradeForbidden) {
		t.Fatalf("downgrade error = %v, want ErrDowngradeForbidden", err)
	}
}
