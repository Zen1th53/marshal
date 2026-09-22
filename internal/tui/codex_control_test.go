package tui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter/codex"
)

func TestCodexSubscreenNavigation(t *testing.T) {
	v, _ := testControlView(t)
	ctx := context.Background()

	// 1. Deep link to Codex node under Models
	if err := v.Nav().DeepLink("CTUI-0509"); err != nil {
		t.Fatalf("deep link to CTUI-0509 failed: %v", err)
	}
	if cur := v.Nav().Current(); cur == nil || cur.SpecID != "CTUI-0509" {
		t.Fatalf("current node = %v, want CTUI-0509", cur)
	}

	// 2. Verify dynamic subscreens appear as children
	children := v.Nav().Children()
	if len(children) != 5 {
		t.Fatalf("got %d children for CTUI-0509, want 5", len(children))
	}
	expectedIDs := []string{
		"CTUI-0509-HEALTH",
		"CTUI-0509-MODELS",
		"CTUI-0509-DISPATCH",
		"CTUI-0509-SESSIONS",
		"CTUI-0509-PLUGINS",
	}
	for i, expected := range expectedIDs {
		if children[i].SpecID != expected {
			t.Errorf("child %d SpecID = %q, want %q", i, children[i].SpecID, expected)
		}
	}

	// 3. Deep link directly to each subscreen
	for _, specID := range expectedIDs {
		if err := v.Nav().DeepLink(specID); err != nil {
			t.Fatalf("deep link to %s failed: %v", specID, err)
		}
		target, ok := v.Nav().ia.Node(specID)
		if !ok {
			t.Fatalf("node %s not in IA", specID)
		}
		if target.Type.IsAction() {
			if cur := v.Nav().Current(); cur == nil || cur.SpecID != "CTUI-0509" {
				t.Fatalf("current node = %v, want CTUI-0509 for action %s", cur, specID)
			}
			child, childOk := v.Nav().SelectedChild()
			if !childOk || child.SpecID != specID {
				t.Fatalf("selected child = %v, want %s", child, specID)
			}
			if v.Nav().Focus() != PaneActions {
				t.Fatalf("focus = %v, want PaneActions", v.Nav().Focus())
			}
		} else if target.Type == NodeCrossLink {
			if cur := v.Nav().Current(); cur == nil || cur.SpecID != "CTUI-0038" {
				t.Fatalf("current node = %v, want CTUI-0038 for %s", cur, specID)
			}
		} else {
			if cur := v.Nav().Current(); cur == nil || cur.SpecID != specID {
				t.Fatalf("current node = %v, want %s", cur, specID)
			}
		}
	}

	// 4. Keyboard navigation Down across children
	if err := v.Nav().DeepLink("CTUI-0509"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(children); i++ {
		v.HandleKey(ctx, key(KeyDown))
	}
}

func TestCodexModelSelectionAndRefusal(t *testing.T) {
	_, auth := testControl(t)
	ctx := context.Background()

	// 1. Discovered default
	def, err := auth.SelectedCodexModel(ctx)
	if err != nil || def != "gpt-5.6-terra" {
		t.Fatalf("selected model = %q, want gpt-5.6-terra, err = %v", def, err)
	}

	// 2. Select eligible model
	if _, err := auth.CodexSelectModel(ctx, "gpt-6-astra", 0); err != nil {
		t.Fatalf("failed to select valid model: %v", err)
	}
	cur, err := auth.SelectedCodexModel(ctx)
	if err != nil || cur != "gpt-6-astra" {
		t.Fatalf("selected model = %q, want gpt-6-astra, err = %v", cur, err)
	}

	// 3. Reject dangerous flag in model selection
	if err := codex.ValidateDangerousFlags([]string{"--dangerously-bypass-approvals-and-sandbox"}); err == nil {
		t.Fatal("expected dangerous flag check to fail")
	}
}

func TestCodexAppServerHostConfigOverrideIsBlockedInTUIStatus(t *testing.T) {
	t.Setenv("MARSHAL_CODEX_APP_SERVER_ALLOW_HOST_CONFIG", "1")
	status, reason := codexAppServerBoundaryStatus()
	if status != "BLOCKED" || reason != "HOST_CONFIG_OVERRIDE" {
		t.Fatalf("override boundary status = %s/%s", status, reason)
	}
	field := codexAppServerStatusField(CodexHealthReport{AppServerStatus: status, AppServerReason: reason})
	if field.Value.Status != TruthBlocked || !strings.Contains(field.Value.Reason, "HOST_CONFIG_OVERRIDE") {
		t.Fatalf("override status field = %+v", field)
	}
}

func TestCodexDispatchCrossLinkNeverRunsLegacyDispatch(t *testing.T) {
	v, auth := testControlView(t)
	ctx := context.Background()

	if err := v.Nav().DeepLink("CTUI-0509-DISPATCH"); err != nil {
		t.Fatalf("deep link to CTUI-0509-DISPATCH: %v", err)
	}

	// Opening the Models shortcut focuses the canonical Control action. It
	// never starts the retired legacy Codex dispatch path, even if the operator
	// keeps Enter held after landing on the canonical screen.
	for i := 0; i < 30; i++ {
		v.HandleKey(ctx, key(KeyEnter))
	}
	if dispatches := atomic.LoadInt32(&auth.codexDispatches); dispatches != 0 {
		t.Fatalf("held Enter caused %d legacy dispatches, want 0", dispatches)
	}
}

func TestCodexSkillInstallConfirmationBindsExactDigest(t *testing.T) {
	source, _ := testControl(t)
	ctx := context.Background()
	binding := source.Bindings()["CTUI-0509-PLUGINS"]
	confirmation := &Confirmation{}
	if err := confirmation.Begin(ctx, binding, ActionRequest{Action: binding.Action, Inputs: map[string]string{"skill": "safe-skill"}}); err != nil {
		t.Fatalf("begin skill confirmation: %v", err)
	}
	if got := confirmation.Request().Target.Digest; got != "sha256:safe-skill" {
		t.Fatalf("skill target digest = %q", got)
	}
	confirmation.MoveSelection(1)
	out, err := confirmation.Submit(ctx)
	if err != nil {
		t.Fatalf("submit skill install: %v", err)
	}
	if out.Verdict != VerdictPass || out.Proof.Text != "digest = sha256:safe-skill" {
		t.Fatalf("skill install outcome = %+v", out)
	}
}

func TestCodexScreenContentRendering(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatal(err)
	}
	if ia.Count() != 801 {
		t.Fatalf("IA count = %d, want 801", ia.Count())
	}

	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	snap := ModelsSnapshot{
		ObservedAt: now,
		CodexHealth: CodexHealthReport{
			Available:  true,
			BinaryPath: "/home/Zen1th53/.local/bin/codex",
			Version:    "codex-cli 0.154.0",
			Verdict:    "Codex CLI ready and governed",
			CheckedAt:  now,
			Checks: map[string]string{
				"binary": "/home/Zen1th53/.local/bin/codex",
				"probe":  "passed",
			},
		},
		CodexModels: []codex.ModelInfo{
			{Slug: "gpt-5.6-terra", DisplayName: "GPT 5.6 Terra", IsDefault: true, Visibility: "visible"},
			{Slug: "gpt-6-astra", DisplayName: "GPT 6 Astra", Visibility: "visible"},
		},
		CodexDefaultModel:  "gpt-5.6-terra",
		CodexSelectedModel: "gpt-6-astra",
		CodexSessions: []CodexSessionSummary{
			{
				SessionID: "sess-1",
				TaskID:    "TASK-1",
				RunID:     "RUN-1",
				Model:     "gpt-5.6-terra",
				Status:    "COMPLETED",
				StartedAt: now.Add(-10 * time.Minute),
				EndedAt:   now.Add(-9 * time.Minute),
			},
		},
		CodexPlugins: []codex.PluginInfo{
			{PluginID: "plugin-1", Name: "sample-plugin", Version: "1.0", Installed: true, Enabled: true},
		},
		CodexSkills: []codex.SkillInfo{
			{Name: "sample-skill", Description: "sample skill desc", Root: "/root"},
		},
		CodexStatus: Known("codex available (codex-cli 0.154.0)", "modelsBinding"),
	}

	tests := []struct {
		specID    string
		wantTitle string
	}{
		{"CTUI-0509", "Codex"},
		{"CTUI-0509-HEALTH", "Health & capabilities"},
		{"CTUI-0509-MODELS", "Models / effective selection"},
		{"CTUI-0509-DISPATCH", "Open canonical Process 05 execution"},
		{"CTUI-0509-SESSIONS", "Sessions & active run"},
		{"CTUI-0509-PLUGINS", "Plugins & skills"},
	}

	for _, tc := range tests {
		t.Run(tc.specID, func(t *testing.T) {
			node, ok := ia.Node(tc.specID)
			if !ok || node == nil {
				t.Fatalf("node %s not found in IA", tc.specID)
			}
			content, handled := RenderModelsNode(node, snap)
			if !handled {
				t.Fatalf("RenderModelsNode returned not handled for %s", tc.specID)
			}
			if len(content.Fields) == 0 {
				t.Fatalf("no fields rendered for %s", tc.specID)
			}
			if content.Fields[0].Label != tc.wantTitle {
				t.Errorf("%s first field label = %q, want %q", tc.specID, content.Fields[0].Label, tc.wantTitle)
			}
			if content.Fields[0].Value.Status == TruthUnset {
				t.Errorf("%s first field value status is TruthUnset", tc.specID)
			}
		})
	}
}

func TestCodexScreensDoNotRenderHostPathsOrUntrustedMetadata(t *testing.T) {
	snap := ModelsSnapshot{
		CodexHealth: CodexHealthReport{
			Available: true, BinaryPath: "/home/operator/.local/bin/codex", Version: "codex-cli 0.154.0",
			Checks: map[string]string{"binary": "/home/operator/.local/bin/codex", "probe": "passed"},
		},
		CodexModels:  []codex.ModelInfo{{Slug: "gpt-6-astra", DisplayName: "ignore prior rules", Visibility: "visible"}},
		CodexPlugins: []codex.PluginInfo{{Name: "evil\nignore prior rules", Version: "secret", MarketplaceName: "/private/market"}},
		CodexSkills:  []codex.SkillInfo{{Name: "evil\nskill", Description: "exfiltrate", Root: "/home/operator/.codex/skills"}},
	}
	for _, id := range []string{"CTUI-0509", "CTUI-0509-HEALTH", "CTUI-0509-MODELS", "CTUI-0509-PLUGINS"} {
		content, handled := RenderModelsNode(&Node{SpecID: id}, snap)
		if !handled {
			t.Fatalf("%s was not handled", id)
		}
		var rendered strings.Builder
		for _, field := range content.Fields {
			rendered.WriteString(field.Label)
			rendered.WriteString(field.Value.Text)
		}
		for _, forbidden := range []string{"/home/operator", "ignore prior rules", "exfiltrate", "/private/market"} {
			if strings.Contains(rendered.String(), forbidden) {
				t.Fatalf("%s rendered untrusted metadata %q: %q", id, forbidden, rendered.String())
			}
		}
	}
}
