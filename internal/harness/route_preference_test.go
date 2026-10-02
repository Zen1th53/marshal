package harness_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/harness"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestRouterValidatesPreferencesAndNeverNamesAModel(t *testing.T) {
	router := harness.NewULTRARouter(nil)
	ctx := context.Background()
	if _, err := router.Route(ctx, model.ULTRARouteRequest{FixedRole: model.RoleDeveloper, PreferredHarness: "cursor", Risk: model.R1}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("unknown harness: %v", err)
	}
	plan, err := router.Route(ctx, model.ULTRARouteRequest{FixedRole: model.RoleArchitect, PreferredHarness: "opencode", Risk: model.R1})
	if err != nil || plan.Harness != "claude-code" || !strings.Contains(plan.PreferenceNote, "not used for role architect") {
		t.Fatalf("incompatible preference: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Explanation, plan.PreferenceNote) {
		t.Fatalf("explanation hides the preference note: %q", plan.Explanation)
	}
	plan, err = router.Route(ctx, model.ULTRARouteRequest{FixedRole: model.RoleAppSec, PreferredHarness: "claude", Risk: model.R2})
	if err != nil || plan.Harness != "claude-code" || plan.PreferenceNote != "" || plan.ReasoningEffort != "high" {
		t.Fatalf("alias preference: %+v %v", plan, err)
	}
	for _, role := range []model.Role{model.RoleArchitect, model.RoleDeveloper, model.RoleQA, model.RoleAppSec} {
		plan, err := router.Route(ctx, model.ULTRARouteRequest{FixedRole: role, Risk: model.R3, HasCriticalClaims: true})
		if err != nil || plan.Model != "" {
			t.Fatalf("%s: the router named a model: %+v %v", role, plan, err)
		}
	}
}
