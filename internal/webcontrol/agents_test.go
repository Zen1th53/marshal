package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT182AgentsListAndDetail(t *testing.T) {
	client := newAuthenticatedTestClient(t, "admin")

	// 1. List agents
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents?provider=claude", nil)
	w := client.Do(req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var paged webcontrol.PagedResponse[webcontrol.AgentSummaryDTO]
	if err := json.NewDecoder(w.Body).Decode(&paged); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if len(paged.Items) != 1 || paged.Items[0].Provider != "claude" {
		t.Fatalf("expected 1 claude agent, got: %+v", paged.Items)
	}

	// 2. Get agent detail
	reqDetail := httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-claude-planner", nil)
	wDetail := client.Do(reqDetail)

	if wDetail.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for agent detail, got %d", wDetail.Code)
	}

	var detail webcontrol.AgentDetailDTO
	if err := json.NewDecoder(wDetail.Body).Decode(&detail); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if detail.ID != "agent-claude-planner" || detail.MemoryContribution.DecisionsLogged != 32 {
		t.Fatalf("unexpected agent detail payload: %+v", detail)
	}

	// 3. IDOR / Not found
	req404 := httptest.NewRequest(http.MethodGet, "/api/v1/agents/non-existent-agent", nil)
	w404 := client.Do(req404)
	if w404.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown agent, got %d", w404.Code)
	}

	// 4. Security scan: verify no secrets leaked in body
	bodyStr := wDetail.Body.String()
	for _, forbidden := range []string{"api_key", "secret", "token", "password", "bearer"} {
		if strings.Contains(strings.ToLower(bodyStr), forbidden) {
			t.Fatalf("secret keyword %q leaked in agent detail payload", forbidden)
		}
	}
}

func TestAgentCRUDLifecycleWeb(t *testing.T) {
	client := newAuthenticatedTestClient(t, "admin")

	// 1. Create Agent
	createPayload := webcontrol.MutationEnvelope[webcontrol.CreateAgentPayload]{
		Payload: webcontrol.CreateAgentPayload{
			ID:           "agent-test-custom",
			Name:         "Custom Testing Agent",
			Role:         "developer",
			Provider:     "claude",
			Model:        "claude-3-7-sonnet",
			Capabilities: []string{"code_edit", "test_execute"},
		},
	}
	body, _ := json.Marshal(createPayload)
	reqCreate := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(string(body)))
	reqCreate.Header.Set("Content-Type", "application/json")
	wCreate := client.Do(reqCreate)
	if wCreate.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for agent creation, got: %d: %s", wCreate.Code, wCreate.Body.String())
	}

	var created webcontrol.AgentDetailDTO
	_ = json.NewDecoder(wCreate.Body).Decode(&created)
	if created.ID != "agent-test-custom" || created.Name != "Custom Testing Agent" {
		t.Fatalf("unexpected created agent: %+v", created)
	}

	// 2. Update Agent
	newName := "Renamed Testing Agent"
	updatePayload := webcontrol.MutationEnvelope[webcontrol.UpdateAgentPayload]{
		ExpectedRevision: 0,
		Payload: webcontrol.UpdateAgentPayload{
			Name: &newName,
		},
	}
	bodyUpdate, _ := json.Marshal(updatePayload)
	reqUpdate := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/agent-test-custom", strings.NewReader(string(bodyUpdate)))
	reqUpdate.Header.Set("Content-Type", "application/json")
	wUpdate := client.Do(reqUpdate)
	if wUpdate.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for agent update, got: %d: %s", wUpdate.Code, wUpdate.Body.String())
	}

	// 3. Delete Agent
	reqDelete := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/agent-test-custom?expected_revision=1", nil)
	wDelete := client.Do(reqDelete)
	if wDelete.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for agent deletion, got: %d: %s", wDelete.Code, wDelete.Body.String())
	}

	// 4. Verify Not Found after deletion
	reqGet := httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-test-custom", nil)
	wGet := client.Do(reqGet)
	if wGet.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found after deletion, got: %d", wGet.Code)
	}
}
