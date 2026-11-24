package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// A refusal caused by a byte cap keeps its old machine code and adds the
// structured limit; a changed-context refusal keeps where the change was
// seen. The message stays a plain sentence with the supported next step.
func TestToolErrorPreservesLimitAndChangedDetails(t *testing.T) {
	limit := model.Limit{Kind: "required_context", Subject: "required project context", ActualBytes: 114689, CapBytes: 114688, Action: "Ask the owner to approve splitting the work into a narrower scope."}
	budget := model.NewLimitError(fmt.Errorf("%w: complete packet requires 114689 bytes (cap 114688)", projectcontext.ErrBudget), limit)
	pack := model.NewLimitError(errors.New("plan pack: tasks/T1.md is larger than 65536 bytes"), model.Limit{Kind: "pack_file", Subject: "plan pack file tasks/T1.md", ActualBytes: 65537, CapBytes: 65536, Action: "Shorten tasks/T1.md."})
	for _, tc := range []struct {
		name, code, kind, detail, message string
		err                               error
	}{
		{"budget", projectcontext.ErrBudget.Error(), "required_context", "complete packet requires 114689 bytes (cap 114688)", "narrower scope", budget},
		{"wrapped budget", projectcontext.ErrBudget.Error(), "required_context", "complete packet requires 114689 bytes (cap 114688)", "narrower scope", fmt.Errorf("verification stopped: %w", budget)},
		{"pack", "limit_exceeded", "pack_file", "", "Shorten tasks/T1.md.", pack},
		{"changed where", projectcontext.ErrChanged.Error(), "", "context.go:411", "Read marshal_project_contract_context again", projectcontext.Changed("context.go:411")},
		{"changed detail", projectcontext.ErrChanged.Error(), "", "goal revision 3 replaced revision 2", "Read marshal_project_contract_context again", fmt.Errorf("%w: goal revision 3 replaced revision 2", projectcontext.ErrChanged)},
	} {
		got := toolError("marshal_project_contract_context", tc.err)
		if got.Data == nil || got.Data.Code != tc.code || got.Data.Detail != tc.detail || !strings.Contains(got.Message, tc.message) {
			t.Fatalf("%s: %+v data %+v", tc.name, got, got.Data)
		}
		if strings.Contains(got.Message, "REQUIRED_CONTEXT") {
			t.Fatalf("%s: raw code in message %q", tc.name, got.Message)
		}
		if tc.kind == "" {
			if got.Data.Limit != nil {
				t.Fatalf("%s: unexpected limit %+v", tc.name, got.Data.Limit)
			}
			continue
		}
		if got.Data.Limit == nil || got.Data.Limit.Kind != tc.kind || !got.Data.Limit.Limited || got.Data.Limit.ActualBytes <= got.Data.Limit.CapBytes || got.Data.Limit.Action == "" {
			t.Fatalf("%s: limit %+v", tc.name, got.Data.Limit)
		}
		raw, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Data struct {
				Code  string         `json:"code"`
				Limit map[string]any `json:"limit"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"kind", "subject", "actualBytes", "capBytes", "limited", "action"} {
			if _, ok := wire.Data.Limit[key]; !ok {
				t.Fatalf("%s: wire limit lacks %s: %s", tc.name, key, raw)
			}
		}
	}
}
