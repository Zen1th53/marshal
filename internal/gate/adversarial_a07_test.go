package gate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/policy"
)

func TestDecisionRejectsMalformedChangeDigest(t *testing.T) {
	decision := Decision{
		DecisionID: "decision-a07", Point: GatePointPrePush, Subject: "agent-a07", Resource: "repo:a07",
		Checks:       []CheckResult{{CheckID: "check", Status: CheckStatusPass}},
		PolicyDigest: policy.PolicyDigest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		ChangeDigest: "sha512:" + strings.Repeat("a", 64), CreatedAt: fixedDecisionTime(),
	}
	if !errors.Is(decision.Validate(), ErrInvalidDecision) {
		t.Fatalf("malformed digest accepted")
	}
}

func TestEngineSanitizesEvaluatorErrorAndDoesNotLeakSecret(t *testing.T) {
	marker := "MARSHAL_TEST_SECRET_T20_A07_4c1e"
	engine, err := NewEngine(EngineConfig{
		PolicyDigest:   policy.PolicyDigest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		RequiredChecks: map[GatePoint][]CheckID{GatePointPrePush: {"hostile-check"}},
		Checks: map[CheckID]CheckFunc{"hostile-check": func(context.Context, CheckRequest) (CheckResult, error) {
			return CheckResult{}, errors.New(marker)
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := engine.Evaluate(context.Background(), GatePointPrePush, "agent-a07", "repo:a07")
	if err == nil || strings.Contains(err.Error(), marker) || strings.Contains(string(decision.PolicyDigest), marker) {
		t.Fatalf("secret leaked decision=%#v err=%v", decision, err)
	}
}

func FuzzDecisionValidationNeverPanics(f *testing.F) {
	f.Add("sha256:" + strings.Repeat("a", 64))
	f.Add("sha512:" + strings.Repeat("b", 64))
	f.Fuzz(func(t *testing.T, changeDigest string) {
		decision := Decision{DecisionID: "decision-fuzz", Point: GatePointPrePush, Subject: "agent", Resource: "repo", Checks: []CheckResult{{CheckID: "check", Status: CheckStatusPass}}, PolicyDigest: policy.PolicyDigest("sha256:" + strings.Repeat("a", 64)), ChangeDigest: changeDigest, CreatedAt: fixedDecisionTime()}
		_ = decision.Validate()
	})
}

func fixedDecisionTime() time.Time { return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC) }
