package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/architecturereview"
)

func architectureServerRuntime(t *testing.T) *Runtime {
	t.Helper()
	root := runtimeRepo(t).Path()
	if _, err := Bootstrap(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

func architectureServerState(t *testing.T, runtime *Runtime) json.RawMessage {
	t.Helper()
	proposal := architectureTestJSON(t, architectureTestProposal())
	views, err := architecturereview.GenericLayouts(proposal)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := architectureReviewDirectory(runtime.layout.RuntimeDir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = architecturereview.New(architecturereview.Config{Context: t.Context(), Project: runtime.ProjectIdentity(), ArchitectureID: "architecture-fixture", StateDir: directory, Proposal: proposal, Layouts: views})
	if err != nil {
		t.Fatal(err)
	}
	return proposal
}

func architectureServerFakeCLI(t *testing.T, runtime *Runtime, response json.RawMessage) (*MarshalCLI, string) {
	t.Helper()
	cli, marker := architectureTestCLI(t, string(response))
	source, err := os.ReadFile(cli.Binary)
	if err != nil {
		t.Fatal(err)
	}
	source = []byte(strings.Replace(string(source), "cat <<'MARSHAL_UNIT_TEST_EVENT'", `printf '%s\n' '{"type":"thread.started","thread_id":"fixture-only-architecture-session"}'`+"\ncat <<'MARSHAL_UNIT_TEST_EVENT'", 1))
	if err := os.WriteFile(cli.Binary, source, 0700); err != nil {
		t.Fatal(err)
	}
	cli.Dir = runtime.layout.Root
	return cli, marker
}

func architectureServerGET(t *testing.T, session *ArchitectureReviewSession) map[string]any {
	t.Helper()
	location, err := url.Parse(session.URL)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, "http://"+location.Host+"/api/review", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+location.Query().Get("token"))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("state status=%d", response.StatusCode)
	}
	var state map[string]any
	if err = json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestArchitectureServerReusesPrivateViewAndSubmitsExactModelFeedback(t *testing.T) {
	runtime := architectureServerRuntime(t)
	before, err := runtime.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	proposal := architectureServerState(t, runtime)
	response := architectureTestJSON(t, map[string]any{"status": "answered", "reply": "Fixture answer", "questions": []string{}, "architecture": proposal})
	cli, marker := architectureServerFakeCLI(t, runtime, response)
	ctx, cancel := context.WithCancel(architectureServerContext(t, runtime, cli))
	defer cancel()
	session, err := runtime.startArchitectureReview(ctx, cli, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	architectureAssertNoModelTurn(t, marker)
	location, _ := url.Parse(session.URL)
	page, err := http.Get(session.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != 200 || !strings.Contains(string(body), "<svg") {
		t.Fatal("built-in schematic page unavailable")
	}
	state := architectureServerGET(t, session)
	layout := state["layouts"].(map[string]any)["architecture"].(map[string]any)
	input := map[string]any{"protocol_version": architecturereview.Version, "action": "submit", "revision": state["revision"], "digest": state["digest"],
		"ui_digest": state["ui_digest"], "language": state["language"],
		"target": map[string]any{"kind": "component", "id": "collector", "diagram_id": "architecture", "layout_version": layout["version"]}, "request_type": "comment", "comment": "Explain this exact collector"}
	raw := architectureTestJSON(t, input)
	request, _ := http.NewRequest(http.MethodPost, "http://"+location.Host+"/api/review", strings.NewReader(string(raw)))
	request.Header.Set("Authorization", "Bearer "+location.Query().Get("token"))
	request.Header.Set("Origin", "http://"+location.Host)
	posted, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	posted.Body.Close()
	if posted.StatusCode != 200 {
		t.Fatalf("submit status=%d", posted.StatusCode)
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		state = architectureServerGET(t, session)
		entries := state["comments"].([]any)
		if len(entries) > 0 && entries[0].(map[string]any)["status"] == "answered" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	entry := state["comments"].([]any)[0].(map[string]any)
	if entry["status"] != "answered" || entry["receipt"].(map[string]any)["session_id"] != "fixture-only-architecture-session" || state["approved"] != false {
		t.Fatalf("feedback not verified precisely: %+v", entry)
	}
	if _, err = os.Stat(marker); err != nil {
		t.Fatal("explicit submit did not invoke fixture model")
	}
	after, err := runtime.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if before.TaskCount != after.TaskCount || before.AgentCount != after.AgentCount || before.SessionCount != after.SessionCount {
		t.Fatal("architecture review created task/agent/execution-session authority")
	}
	urlInfo, err := os.Stat(filepath.Join(runtime.layout.RuntimeDir, "architecture", "review", "url.txt"))
	if err != nil || urlInfo.Mode().Perm() != 0600 {
		t.Fatalf("URL token file is public: %v %v", urlInfo, err)
	}
	cancel()
	select {
	case <-session.Done:
	case <-time.After(3 * time.Second):
		t.Fatal("architecture server outlived its owner context")
	}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestArchitectureServerFreshDraftBuildsActualTopologyAndDisplayBeforeOpening(t *testing.T) {
	runtime := architectureServerRuntime(t)
	proposal := architectureTestProposal()
	stringsUI := []any{}
	for key, text := range architecturereview.EnglishUIStrings() {
		stringsUI = append(stringsUI, map[string]any{"key": key, "text": text})
	}
	components := []any{}
	for _, value := range proposal["components"].([]any) {
		c := value.(map[string]any)
		components = append(components, map[string]any{"id": c["id"], "name": c["name"], "responsibility": c["responsibility"]})
	}
	connections := []any{}
	for _, value := range proposal["connections"].([]any) {
		e := value.(map[string]any)
		connections = append(connections, map[string]any{"id": e["id"], "label": e["label"], "caption": e["label"]})
	}
	ui := architectureTestJSON(t, map[string]any{"language": "English", "strings": stringsUI, "components": components, "connections": connections})
	responses := []json.RawMessage{architectureTestJSON(t, proposal), ui}
	cli, marker := architectureServerFakeCLI(t, runtime, responses[0])
	script := "#!/bin/sh\nprintf c >> \"$0.calls\"\ncount=$(wc -c < \"$0.calls\")\nprintf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"fixture-only-architecture-session\"}'\ncase \"$count\" in\n"
	for index, response := range responses {
		event := architectureTestJSON(t, map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": string(response)}})
		script += string(rune('1'+index)) + " ) cat <<'UNIT_TEST_EVENT'\n" + string(event) + "\nUNIT_TEST_EVENT\n;;\n"
	}
	script += "*) exit 88;;\nesac\n"
	if err := os.WriteFile(cli.Binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	session, err := runtime.startArchitectureReview(architectureServerContext(t, runtime, cli), cli, "Account for local host traffic")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	state := architectureServerGET(t, session)
	if state["approved"] != false || state["language"] != "English" || state["project"] != runtime.ProjectIdentity() {
		t.Fatalf("fresh draft gained acceptance or lost owner binding: %+v", state)
	}
	layout := state["layouts"].(map[string]any)["architecture"].(map[string]any)
	if len(layout["nodes"].([]any)) != 2 || len(layout["edges"].([]any)) != 1 {
		t.Fatal("actual component and edge topology did not reach schematic")
	}
	if calls, err := os.ReadFile(marker); err != nil || string(calls) != "cc" {
		t.Fatalf("draft and localized UI roles not called exactly once: %s %v", calls, err)
	}
}

func TestArchitectureServerNeverSilentlyReplacesExistingOwnerGraph(t *testing.T) {
	runtime := architectureServerRuntime(t)
	proposal := architectureServerState(t, runtime)
	cli, marker := architectureServerFakeCLI(t, runtime, proposal)
	if _, err := runtime.startArchitectureReview(t.Context(), cli, "Make another unrelated architecture"); err == nil {
		t.Fatal("explicit goal silently replaced existing owner graph")
	}
	architectureAssertNoModelTurn(t, marker)
	if _, err := runtime.StartArchitectureReview(t.Context(), "unsupported-provider", ""); err == nil {
		t.Fatal("unselected/unsupported provider accepted")
	}
}

func TestArchitectureServerLeasePreventsDuplicateWritersBeforeModelGeneration(t *testing.T) {
	runtime := architectureServerRuntime(t)
	lease, err := runtime.AcquireArchitectureReviewLease()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	directory := filepath.Join(runtime.layout.RuntimeDir, "architecture", "review")
	if err := writeArchitectureURL(filepath.Join(directory, "url.txt"), "http://127.0.0.1:34567/?token=fixture-only-token-long-enough-32characters"); err != nil {
		t.Fatal(err)
	}
	proposal := architectureTestJSON(t, architectureTestProposal())
	cli, marker := architectureServerFakeCLI(t, runtime, proposal)
	if _, err := runtime.startArchitectureReview(t.Context(), cli, "Build a new architecture"); err == nil || !strings.Contains(err.Error(), "http://127.0.0.1:34567/") {
		t.Fatalf("duplicate writer not pointed to current view: %v", err)
	}
	architectureAssertNoModelTurn(t, marker)
	if err := writeArchitectureURL(filepath.Join(directory, "url.txt"), "https://outside.example/?token=fixture-only-token-long-enough-32characters"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.AcquireArchitectureReviewLease(); err == nil || strings.Contains(err.Error(), "outside.example") {
		t.Fatalf("foreign URL surfaced as local view: %v", err)
	}
	lease.Close()
	if second, err := runtime.AcquireArchitectureReviewLease(); err != nil {
		t.Fatal(err)
	} else {
		second.Close()
	}
}

func TestArchitectureServerPublicEntryReusesStateWithoutGrantingAuthority(t *testing.T) {
	runtime := architectureServerRuntime(t)
	architectureServerState(t, runtime)
	session, err := runtime.StartArchitectureReview(t.Context(), "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	if state := architectureServerGET(t, session); state["approved"] != false {
		t.Fatal("public view fabricated owner acceptance")
	}
	if _, err := runtime.StartArchitectureReview(t.Context(), "codex", ""); !errors.Is(err, ErrRuntimeOwned) {
		t.Fatalf("public duplicate writer accepted: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	var absent *Runtime
	if _, err := absent.StartArchitectureReview(t.Context(), "codex", ""); err == nil {
		t.Fatal("nil runtime opened review")
	}
	var absentSession *ArchitectureReviewSession
	if err := absentSession.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestArchitectureServerRejectsCorruptViewAndReleasesFailedGenerationLease(t *testing.T) {
	runtime := architectureServerRuntime(t)
	proposal := architectureServerState(t, runtime)
	cli, marker := architectureServerFakeCLI(t, runtime, proposal)
	statePath := filepath.Join(runtime.layout.RuntimeDir, "architecture", "review", "state.json")
	if err := os.WriteFile(statePath, []byte(`{"protocol_version":"architecture-review.v1","project":"foreign"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.startArchitectureReview(t.Context(), cli, ""); err == nil {
		t.Fatal("corrupt foreign view accepted")
	}
	architectureAssertNoModelTurn(t, marker)
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.startArchitectureReview(t.Context(), cli, "Fresh architecture"); err == nil {
		t.Fatal("architecture incorrectly accepted as a UI translation")
	}
	if lease, err := runtime.AcquireArchitectureReviewLease(); err != nil {
		t.Fatalf("failed generation leaked its kernel lease: %v", err)
	} else {
		lease.Close()
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatal("failed generation persisted an accepted/partial architecture")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := runtime.startArchitectureReview(ctx, cli, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled generation proceeded: %v", err)
	}
}

func TestArchitectureServerPrivateFilesCannotRedirectTokensOrPreferences(t *testing.T) {
	runtime := architectureServerRuntime(t)
	directory, err := architectureReviewDirectory(runtime.layout.RuntimeDir)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(out, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "url.txt")
	if err := os.Symlink(out, path); err != nil {
		t.Fatal(err)
	}
	if err := writeArchitectureURL(path, "private-token"); err == nil {
		t.Fatal("URL token redirected through symlink")
	}
	if got, err := os.ReadFile(out); err != nil || string(got) != "preserve" {
		t.Fatal("URL token modified outside file")
	}
	if err := os.Symlink(out, filepath.Join(directory, "runtime-owner.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.AcquireArchitectureReviewLease(); err == nil {
		t.Fatal("ownership lease followed a symlink")
	}
	intake := filepath.Join(runtime.layout.RuntimeDir, "marshal-intake.json")
	if err := os.WriteFile(intake, []byte(`{"language":"English"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ApprovedMarshalLanguage(); err == nil {
		t.Fatal("public preference accepted")
	}
	if err := os.Chmod(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.AcquireArchitectureReviewLease(); err == nil {
		t.Fatal("public owner state directory accepted")
	}
}

func TestArchitectureServerInvalidRuntimeOrModelCannotCreateReviewState(t *testing.T) {
	runtime := architectureServerRuntime(t)
	if _, err := runtime.startArchitectureReview(t.Context(), nil, ""); err == nil {
		t.Fatal("missing model created review")
	}
	foreign := &Runtime{store: runtime.store, layout: runtime.layout}
	foreign.layout.RuntimeDir = filepath.Join(runtime.layout.Root, "foreign-state")
	if _, err := foreign.startArchitectureReview(t.Context(), &MarshalCLI{Provider: "codex"}, ""); err == nil {
		t.Fatal("foreign runtime state accepted")
	}
	if _, err := foreign.AcquireArchitectureReviewLease(); err == nil {
		t.Fatal("foreign runtime obtained writer lease")
	}
	var absent *Runtime
	if _, err := absent.AcquireArchitectureReviewLease(); err == nil {
		t.Fatal("missing runtime obtained writer lease")
	}
	if _, err := absent.ApprovedMarshalLanguage(); err == nil {
		t.Fatal("missing runtime invented a saved owner language")
	}
	if err := writeArchitectureURL(filepath.Join(runtime.layout.RuntimeDir, "missing-parent", "url.txt"), "private-token"); err == nil {
		t.Fatal("private URL write succeeded without owned directory")
	}
}

func TestArchitectureServerCloseCancelsAndJoinsRunningModel(t *testing.T) {
	runtime := architectureServerRuntime(t)
	architectureServerState(t, runtime)
	cli, _ := architectureServerFakeCLI(t, runtime, nil)
	if err := os.WriteFile(cli.Binary, []byte("#!/bin/sh\nprintf called > \"$0.started\"\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	session, err := runtime.startArchitectureReview(architectureServerContext(t, runtime, cli), cli, "")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	state := architectureServerGET(t, session)
	location, _ := url.Parse(session.URL)
	layout := state["layouts"].(map[string]any)["architecture"].(map[string]any)
	input := map[string]any{"protocol_version": architecturereview.Version, "action": "submit", "revision": state["revision"], "digest": state["digest"], "ui_digest": state["ui_digest"], "language": state["language"],
		"target": map[string]any{"kind": "component", "id": "collector", "diagram_id": "architecture", "layout_version": layout["version"]}, "request_type": "comment", "comment": "Explain the exact collector"}
	request, _ := http.NewRequest(http.MethodPost, "http://"+location.Host+"/api/review", strings.NewReader(string(architectureTestJSON(t, input))))
	request.Header.Set("Authorization", "Bearer "+location.Query().Get("token"))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("submit status=%d", response.StatusCode)
	}
	started := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(cli.Binary + ".started"); err == nil {
			started = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !started {
		t.Fatal("fixture model never began")
	}
	closed := make(chan error, 1)
	go func() { closed <- session.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close abandoned or waited for uncancelled model process")
	}
	select {
	case <-session.Done:
	default:
		t.Fatal("closed server still considered live")
	}
	if lease, err := runtime.AcquireArchitectureReviewLease(); err != nil {
		t.Fatal("completed shutdown retained its writer lease")
	} else {
		lease.Close()
	}
}

func TestApprovedMarshalLanguageUsesOnlySavedOwnerChoice(t *testing.T) {
	runtime := architectureServerRuntime(t)
	t.Setenv("LANG", "uz_UZ.UTF-8")
	if language, err := runtime.ApprovedMarshalLanguage(); err != nil || language != "English" {
		t.Fatalf("locale changed default language: %q %v", language, err)
	}
	path := filepath.Join(runtime.layout.RuntimeDir, "marshal-intake.json")
	for _, language := range []string{"English", "Uzbek", "Russian", "Owner custom language", "  "} {
		raw := architectureTestJSON(t, map[string]any{"language": language, "earlier_work": "no"})
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		want := language
		if strings.TrimSpace(want) == "" {
			want = "English"
		}
		if got, err := runtime.ApprovedMarshalLanguage(); err != nil || got != want {
			t.Fatalf("saved owner choice %q became %q %v", language, got, err)
		}
	}
	for _, bad := range []string{`{"language":"Uzbek","language":"English"}`, `{"language":"English\nInjected"}`, `{"language":null}`, `{"language":true}`, `{`} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.ApprovedMarshalLanguage(); err == nil {
			t.Fatalf("malformed preference fell back silently: %s", bad)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing.json"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ApprovedMarshalLanguage(); err == nil {
		t.Fatal("symlink preference followed or silently defaulted")
	}
}
