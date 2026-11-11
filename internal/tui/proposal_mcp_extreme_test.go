//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func bindWorkspaceMCP(t *testing.T, w *Workspace) proposalmcp.Config {
	t.Helper()
	provider := "codex"
	if a := w.tmuxActiveWins["marshal-chat"]; a != nil && a.provider != "" {
		provider = a.provider
	}
	_, env, dir, err := w.prepareProposalMCP(provider, w.runtime.ProjectRoot(), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var path string
	for _, e := range env {
		if strings.HasPrefix(e, "MARSHAL_PROPOSAL_MCP_CONFIG=") {
			path = strings.TrimPrefix(e, "MARSHAL_PROPOSAL_MCP_CONFIG=")
		}
	}
	cfg, err := proposalmcp.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	a := w.tmuxActiveWins["marshal-chat"]
	if a == nil {
		a = &activeTmuxAgent{id: "marshal-chat", provider: provider, paneID: "%42", sessionID: "native-session"}
		w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": a}
	}
	a.role = "marshal-chat"
	a.state = "working"
	a.briefingDir = dir
	w.activateProposalMCP(a)
	t.Cleanup(dir.remove)
	return cfg
}
func liveMCPFixture(t *testing.T) (*Workspace, *proposalMCPLease, proposalmcp.Config) {
	t.Helper()
	w, _ := realControlWorkspace(t, "SESSION-live-mcp", false)
	cfg := bindWorkspaceMCP(t, w)
	return w, w.proposalMCP.active, cfg
}
func submitMCP(t *testing.T, cfg proposalmcp.Config, raw string) proposalMCPReceipt {
	t.Helper()
	out, err := proposalmcp.Call(t.Context(), cfg, "submit", json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	var r proposalMCPReceipt
	if err = json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	return r
}
func TestLiveMCPProposalCanonicalApplicationAndIdempotency(t *testing.T) {
	w, l, cfg := liveMCPFixture(t)
	raw := `{"submission_id":"setting-1","proposal": {"action": "setting", "key": "acceptance-mode", "value": "marshal"}}`
	r := submitMCP(t, cfg, raw)
	if r.State != "admitted" {
		t.Fatalf("premature application: %+v", r)
	}
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].SourceLaunch != l.id {
		t.Fatalf("request missing launch provenance: %+v", batch)
	}
	if !w.routeBuildPermission(t.Context(), batch[0]) {
		t.Fatal("not applied by normal authority pipeline")
	}
	settings, err := w.store.GetMarshalSettings(t.Context(), w.projectID)
	if err != nil || string(settings.Value.AcceptanceMode) != "marshal" {
		t.Fatalf("canonical settings: %+v %v", settings, err)
	}
	duplicate := submitMCP(t, cfg, raw)
	if duplicate.State != "applied" || duplicate.OccurrenceID != r.OccurrenceID {
		t.Fatalf("receipt: %+v", duplicate)
	}
	if !w.permissions.queue.Empty() {
		t.Fatal("duplicate queued another action")
	}
	if _, err := proposalmcp.Call(t.Context(), cfg, "submit", json.RawMessage(strings.Replace(raw, "marshal\"}", "user\"}", 1))); err == nil {
		t.Fatal("conflicting duplicate accepted")
	}
	stored, err := w.store.MCPProposal(t.Context(), w.projectID, r.OccurrenceID)
	if err != nil || stored.State != "applied" || stored.Payload != `{"action": "setting", "key": "acceptance-mode", "value": "marshal"}` {
		t.Fatalf("durable exact-byte source: %+v %v", stored, err)
	}
}
func TestLiveMCPRevocationStaleRecoveryAndQueueIsolation(t *testing.T) {
	w, l, cfg := liveMCPFixture(t)
	r := submitMCP(t, cfg, `{"submission_id":"pending","proposal":{"action":"setting","key":"acceptance-mode","value":"marshal"}}`)
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 {
		t.Fatal(batch)
	}
	old := batch[0]
	w.revokeProposalLease(l)
	if err := w.decidePermission(t.Context(), old, true, "test"); err == nil {
		t.Fatal("stale deferred effect applied")
	}
	if !w.staleProposalMCP(r.OccurrenceID) {
		t.Fatal("stale record recoverable")
	}
	stored, err := w.store.MCPProposal(t.Context(), w.projectID, r.OccurrenceID)
	if err != nil || stored.State != "expired" {
		t.Fatalf("expiry: %+v %v", stored, err)
	}
	newer := old
	newer.SourceLaunch = "new-launch"
	if newer.Key() == old.Key() {
		t.Fatal("queue crossed launch boundaries")
	}
}
func TestLiveMCPDecisionAcceptanceAndApplicationSeparate(t *testing.T) {
	marshalDecidesWork(t)
	w, l, cfg := liveMCPFixture(t)
	req := permission.Request{Kind: "read", Object: t.TempDir(), Scope: "session", Reason: "inspect"}
	w.permissions.build.pending = map[string]*buildPendingDecision{"pending-decision": {request: req, provider: "codex", launchID: l.id, deadline: time.Now().Add(time.Minute)}}
	raw := `{"submission_id":"answer","proposal":{"action":"decide","id":"pending-decision","allow":false,"reason":"scope denied"}}`
	r := submitMCP(t, cfg, raw)
	if r.State != "answer_accepted" {
		t.Fatalf("acceptance confused with application: %+v", r)
	}
	retry := submitMCP(t, cfg, raw)
	if retry.OccurrenceID != r.OccurrenceID {
		t.Fatal("answer replay changed receipt")
	}
	if !w.routeBuildPermission(t.Context(), req) {
		t.Fatal("answer not consumed")
	}
	stored, err := w.store.MCPProposal(t.Context(), w.projectID, r.OccurrenceID)
	if err != nil || stored.State != "denied" {
		t.Fatalf("final outcome: %+v %v", stored, err)
	}
}
func TestLiveMCPStrictArgumentsSessionAndUntrustedSources(t *testing.T) {
	w, l, cfg := liveMCPFixture(t)
	for _, raw := range []string{
		`{"submission_id":"x","submission_id":"y","proposal":{"action":"intake","language":"uz"}}`,
		`{"submission_id":"x","proposal":{"action":"intake","language":"uz","language":"en"}}`,
		`{"submission_id":"x","project_id":"foreign","proposal":{"action":"intake","language":"uz"}}`,
		`{"submission_id":"x","proposal":{"action":"decide","id":"unknown","allow":true,"reason":"x"}}`,
	} {
		if _, err := proposalmcp.Call(t.Context(), cfg, "submit", json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	w.tmuxMu.Lock()
	w.tmuxActiveWins["marshal-chat"].sessionID = "foreign-session"
	w.tmuxMu.Unlock()
	if _, err := proposalmcp.Call(t.Context(), cfg, "submit", json.RawMessage(`{"submission_id":"x","proposal":{"action":"intake","language":"uz"}}`)); err == nil {
		t.Fatal("foreign native session accepted")
	}
	l.active = false
	if _, err := w.handleProposalMCP(context.Background(), l, "receipt", json.RawMessage(`{"submission_id":"x"}`)); err == nil {
		t.Fatal("dormant/adopted launch accepted")
	}
	if w.consumeBuildDecision([]byte(`{"action":"decide","id":"unknown","allow":true,"reason":"file"}`)) {
		t.Fatal("untyped decision accepted")
	}
}
func TestLiveMCPApplyRevocationSerialization(t *testing.T) {
	w, l, cfg := liveMCPFixture(t)
	submitMCP(t, cfg, `{"submission_id":"serial","proposal":{"action":"setting","key":"acceptance-mode","value":"marshal"}}`)
	req := w.permissions.queue.Take(false)[0]
	finish, err := w.beginProposalMCPApply(req)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan struct{})
	go func() { close(started); w.revokeProposalLease(l); close(done) }()
	<-started
	if err := finish(false, errors.New("test refused application")); err != nil {
		t.Fatal(err)
	}
	<-done
	if _, err := w.beginProposalMCPApply(req); err == nil {
		t.Fatal("effect admitted after revocation")
	}
}

// This fake provider reads the real generated per-launch configuration and
// starts a genuine separate MCP child with that explicit path, even when the
// native environment has been filtered. It does not inject a copied capability.
func nativeMCPChildConfigPath(t *testing.T, provider string, args, env []string, dir *briefingDir) string {
	t.Helper()
	if provider == "codex" {
		prefix := "mcp_servers.marshal.env.MARSHAL_PROPOSAL_MCP_CONFIG="
		var path string
		count := 0
		for i, a := range args {
			if a == "-c" && i+1 < len(args) && strings.HasPrefix(args[i+1], prefix) {
				count++
				var err error
				path, err = strconv.Unquote(strings.TrimPrefix(args[i+1], prefix))
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		if count != 1 || path == "" {
			t.Fatal("Codex missing exact per-launch child binding")
		}
		return path
	}
	var data []byte
	if provider == "claude" {
		var err error
		data, err = os.ReadFile(filepath.Join(dir.path, "claude_mcp.json"))
		if err != nil {
			t.Fatal(err)
		}
	} else {
		for _, e := range env {
			if strings.HasPrefix(e, "OPENCODE_CONFIG_CONTENT=") {
				data = []byte(strings.TrimPrefix(e, "OPENCODE_CONFIG_CONTENT="))
				break
			}
		}
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	group, envKey := "mcpServers", "env"
	if provider == "opencode" {
		group, envKey = "mcp", "environment"
	}
	servers, ok := cfg[group].(map[string]any)
	if !ok {
		t.Fatal("missing provider server map")
	}
	server, ok := servers["marshal"].(map[string]any)
	if !ok {
		t.Fatal("missing Marshal MCP server")
	}
	childEnv, ok := server[envKey].(map[string]any)
	if !ok {
		t.Fatal("missing explicit child environment")
	}
	path, ok := childEnv["MARSHAL_PROPOSAL_MCP_CONFIG"].(string)
	if !ok || path == "" {
		t.Fatal("missing explicit child binding path")
	}
	return path
}
func TestLiveMCPNativeProviderExplicitChildEnvironmentBinding(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "opencode"} {
		t.Run(provider, func(t *testing.T) {
			w, _ := realControlWorkspace(t, "SESSION-launch-environment", false)
			args, env, dir, err := prepareMarshalLaunch(provider, w.runtime.ProjectRoot(), nil, "MARSHAL PROTOCOL\nTest launch", false)
			if err != nil {
				t.Fatal(err)
			}
			args, env, dir, err = w.prepareProposalMCP(provider, w.runtime.ProjectRoot(), args, env, dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(dir.remove)
			path := nativeMCPChildConfigPath(t, provider, args, env, dir)
			matched := false
			for _, e := range env {
				if e == "MARSHAL_PROPOSAL_MCP_CONFIG="+path {
					matched = true
				}
			}
			if !matched {
				t.Fatal("native and MCP child configs differ")
			}
			cfg, err := proposalmcp.LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ProjectRoot != w.runtime.ProjectRoot() || cfg.ProjectID != w.projectID {
				t.Fatal("launch config lost canonical scope")
			}
			a := &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", provider: provider, paneID: "%42", sessionID: "native-session", state: "working", briefingDir: dir}
			w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": a}
			w.activateProposalMCP(a)
			t.Setenv("MARSHAL_PROPOSAL_MCP_CONFIG", "")
			child := memoryChildWithConfigPath(t, w.runtime.ProjectRoot(), path)
			r := decodeMemoryReceipt(t, child.call(t, "memory_remember", map[string]string{"submission_id": "explicit-env", "title": "bound native knowledge", "body": "Exact per-launch config reaches the filtered MCP child"}, true))
			if r.Persisted {
				t.Fatal("submission silently retained memory")
			}
			authorizeTestMemory(t, w, cfg, true)
			r = decodeMemoryReceipt(t, child.call(t, "marshal_memory_receipt", map[string]string{"submission_id": "explicit-env"}, true))
			if !r.Persisted || !r.Indexed {
				t.Fatal("legitimate separate decision did not retain explicit-env candidate")
			}
			// A replacement launch mints a different path and revokes the old transport.
			old := cfg
			newArgs, newEnv, newDir, err := w.prepareProposalMCP(provider, w.runtime.ProjectRoot(), nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(newDir.remove)
			newPath := nativeMCPChildConfigPath(t, provider, newArgs, newEnv, newDir)
			newer, err := proposalmcp.LoadConfig(newPath)
			if err != nil {
				t.Fatal(err)
			}
			a.briefingDir = newDir
			w.activateProposalMCP(a)
			if newer.Endpoint == old.Endpoint {
				t.Fatal("replacement silently reused source lease")
			}
			if _, err = proposalmcp.LoadConfig(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("replacement left old capability file")
			}
			child.call(t, "memory_remember", map[string]string{"title": "after replacement", "body": "must never bind stale native launch"}, false)
			w.closeProposalMCP()
			if _, err = proposalmcp.LoadConfig(newPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("closed runtime left replacement capability file")
			}
			child.call(t, "memory_remember", map[string]string{"title": "after closure", "body": "must never bind closed runtime"}, false)
		})
	}
}

func TestLiveMCPCodexBindingNotAddedWithoutAttachedMarshal(t *testing.T) {
	root := t.TempDir()
	for _, provider := range []string{"codex", "opencode", "claude", "agy"} {
		w := &Workspace{}
		var owned *briefingDir
		if provider == "agy" {
			var e error
			owned, e = newBriefingDir(root, provider)
			if e != nil {
				t.Fatal(e)
			}
			defer owned.remove()
		}
		args, env, dir, err := w.prepareProposalMCP(provider, root, []string{"Start."}, nil, owned)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.Join(args, " "), "MARSHAL_PROPOSAL_MCP_CONFIG") || len(env) != 0 || dir != owned {
			t.Fatalf("unattached source received launch capability: %s", provider)
		}
	}
	for _, provider := range []string{"codex", "claude", "opencode", "agy"} {
		args, env, cleanup, err := ConfigureWorkerMCP(provider, root, "TASK-own", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.Join(append(args, env...), " "), "MARSHAL_PROPOSAL_MCP_CONFIG") {
			t.Fatalf("%s worker configuration received Marshal launch binding", provider)
		}
		cleanup()
	}
}
func TestLiveMCPIntakeLanguageOnlyAccepted(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	if err := w.saveMarshalIntake(marshalIntake{Language: "English", EarlierWork: "yes"}); err != nil {
		t.Fatal(err)
	}
	r := submitMCP(t, cfg, `{"submission_id":"lang-only","proposal":{"action":"intake","language":"Uzbek"}}`)
	if r.State != "applied" {
		t.Fatalf("language-only intake not applied over MCP: %+v", r)
	}
	data, err := os.ReadFile(filepath.Join(w.runtime.ProjectRoot(), statepath.DirName, "marshal-intake.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved marshalIntake
	if err := json.Unmarshal(data, &saved); err != nil || saved.Language != "Uzbek" || saved.EarlierWork != "yes" {
		t.Fatalf("saved intake = %+v %v, want Uzbek with earlier_work kept", saved, err)
	}
	if _, err := proposalmcp.Call(t.Context(), cfg, "submit", json.RawMessage(`{"submission_id":"lang-extra","proposal":{"action":"intake","language":"English","x":"1"}}`)); err == nil {
		t.Fatal("intake with unknown key accepted over MCP")
	}
}
