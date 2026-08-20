package webcontrol_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT186TaskMutationsIdempotencyAndCAS(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// Setup session and CSRF token via login
	code, _ := server.Sessions().CreateOneTimeCode("operator", "admin")
	loginPayload, _ := json.Marshal(map[string]string{"code": code})
	reqLogin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginPayload))
	wLogin := httptest.NewRecorder()
	server.Handler().ServeHTTP(wLogin, reqLogin)
	cookie := wLogin.Result().Cookies()[0]

	reqCSRF := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	reqCSRF.AddCookie(cookie)
	wCSRF := httptest.NewRecorder()
	server.Handler().ServeHTTP(wCSRF, reqCSRF)
	var csrfResp map[string]string
	_ = json.NewDecoder(wCSRF.Body).Decode(&csrfResp)
	csrfToken := csrfResp["csrf_token"]

	// 1. Create task with Idempotency Key
	idempKey := "idem-test-key-001"
	createPayload := webcontrol.MutationEnvelope[webcontrol.CreateTaskPayload]{
		IdempotencyKey: idempKey,
		Payload: webcontrol.CreateTaskPayload{
			Title:       "Test New Automated Task",
			Description: "Deterministic execution plan",
			Risk:        "HIGH",
			AssignedTo:  "agent-claude-planner",
		},
	}
	bodyBytes, _ := json.Marshal(createPayload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrfToken)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}

	var created webcontrol.TaskDetailDTO
	_ = json.NewDecoder(w.Body).Decode(&created)
	firstID := created.ID
	if firstID == "" {
		t.Fatal("expected non-empty created task ID")
	}

	// 2. Replay same request with same idempotency key -> Should return existing task
	reqReplay := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(bodyBytes))
	reqReplay.Header.Set("Content-Type", "application/json")
	reqReplay.Header.Set("X-CSRF-Token", csrfToken)
	reqReplay.AddCookie(cookie)
	wReplay := httptest.NewRecorder()
	server.Handler().ServeHTTP(wReplay, reqReplay)

	if wReplay.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on idempotent replay, got %d", wReplay.Code)
	}

	// 3. CAS revision conflict on update
	updatePayload := webcontrol.MutationEnvelope[webcontrol.UpdateTaskPayload]{
		ExpectedRevision: 999, // Stale revision
		Payload:          webcontrol.UpdateTaskPayload{},
	}
	updateBytes, _ := json.Marshal(updatePayload)
	reqUpdate := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+firstID, bytes.NewReader(updateBytes))
	reqUpdate.Header.Set("Content-Type", "application/json")
	reqUpdate.Header.Set("X-CSRF-Token", csrfToken)
	reqUpdate.AddCookie(cookie)
	wUpdate := httptest.NewRecorder()
	server.Handler().ServeHTTP(wUpdate, reqUpdate)

	if wUpdate.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on stale CAS revision, got %d", wUpdate.Code)
	}

	// 4. Dependency cycle detection
	cyclePayload := webcontrol.MutationEnvelope[webcontrol.UpdateTaskPayload]{
		Payload: webcontrol.UpdateTaskPayload{
			Dependencies: []string{"TASK-002-CONTROL-PLANE"}, // TASK-002 depends on TASK-001; making TASK-001 depend on TASK-002 creates cycle
		},
	}
	cycleBytes, _ := json.Marshal(cyclePayload)
	reqCycle := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/TASK-001-CORE-MEMORY", bytes.NewReader(cycleBytes))
	reqCycle.Header.Set("Content-Type", "application/json")
	reqCycle.Header.Set("X-CSRF-Token", csrfToken)
	reqCycle.AddCookie(cookie)
	wCycle := httptest.NewRecorder()
	server.Handler().ServeHTTP(wCycle, reqCycle)

	if wCycle.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request on circular dependency, got %d", wCycle.Code)
	}
}
