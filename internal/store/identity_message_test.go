package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestRegisterAgentNamesTheMissingField(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	cases := []struct {
		name  string
		agent model.Agent
		want  []string
	}{
		{"no name", model.Agent{ID: "AGENT-1", ProjectID: "P", Role: model.RoleDeveloper}, []string{"--name"}},
		{"bad role", model.Agent{ID: "AGENT-1", ProjectID: "P", DisplayName: "x", Role: "worker"}, []string{`"worker"`, "developer", "reviewer", "--role"}},
	}
	for _, c := range cases {
		err := s.RegisterAgent(ctx, c.agent)
		if !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("%s: want ErrInvalid, got %v", c.name, err)
		}
		for _, want := range c.want {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%s: %q missing from %q", c.name, want, err)
			}
		}
		if strings.Count(err.Error(), "invalid input") > 1 {
			t.Fatalf("%s: doubled prefix in %q", c.name, err)
		}
	}
}
