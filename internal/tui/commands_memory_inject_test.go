package tui

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/model"
	"strings"
	"testing"
	"time"
)

func TestMemoryInjectListsEveryProvider(t *testing.T) {
	for _, channel := range []injectChannel{injectAuto, injectSystemPrompt, injectProjectDoc, injectPrompt, injectOff} {
		t.Run(string(channel), func(t *testing.T) {
			ws := NewWorkspace(nil, "proj", "sess")
			ws.workDir = t.TempDir()
			h := &CommandHandler{ws: ws}
			confirmation, err := h.handleMemoryInject(context.Background(), []string{string(channel)})
			if err != nil {
				t.Fatal(err)
			}
			status, err := h.handleMemoryInject(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, provider := range []string{"claude", "codex", "opencode", "antigravity"} {
				resolved, note := resolveInjectChannel(provider, channel)
				name := providerDisplayName(provider)
				if !strings.Contains(status, fmt.Sprintf("  %-17s %s\n", name+" uses:", resolved)) {
					t.Errorf("status lacks %s via %s: %s", name, resolved, status)
				}
				if !strings.Contains(confirmation, name+": "+string(resolved)) {
					t.Errorf("confirmation lacks %s via %s: %s", name, resolved, confirmation)
				}
				if note != "" && (!strings.Contains(status, note) || !strings.Contains(confirmation, note)) {
					t.Errorf("missing fallback note %q in status or confirmation", note)
				}
			}
		})
	}
}

func TestMemoryInjectPreviewEveryProvider(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess")
	ws.workDir = t.TempDir()
	h := &CommandHandler{ws: ws}
	for _, provider := range []string{"claude", "codex", "opencode", "agy", "antigravity"} {
		t.Run(provider, func(t *testing.T) {
			got, err := h.handleMemoryInject(context.Background(), []string{"preview", provider})
			canonical := provider
			if canonical == "agy" {
				canonical = "antigravity"
			}
			want := "No other provider has recorded work in this project, so " + canonical + " would receive no briefing."
			if err != nil || got != want {
				t.Fatalf("preview = %q, %v; want %q", got, err, want)
			}
		})
	}
	for _, args := range [][]string{{"preview", "unknown"}, {"preview", "claude", "extra"}} {
		got, err := h.handleMemoryInject(context.Background(), args)
		if err != nil || !strings.Contains(got, "[claude|codex|opencode|agy|antigravity]") {
			t.Errorf("usage = %q, %v", got, err)
		}
	}
}

func TestMemoryInjectPreviewBriefingAndFallback(t *testing.T) {
	st, ws, ctx := newMutationWorkspace(t)
	ws.workDir = t.TempDir()
	now := time.Now().UTC()
	for _, provider := range []string{"claude", "codex", "opencode", "antigravity"} {
		err := st.WriteMemoryV2(ctx, model.MemoryRecordV2{
			ID: "MEM-inject-" + provider, ProjectID: "PROJECT-mut",
			Kind: model.MemoryKindDecision, Lifecycle: model.MemoryDurable, Authority: model.AuthorityVerified,
			Title: "Recorded work", Body: "Completed work from " + provider,
			Scope: string(model.ScopeSession), ScopeID: "sess-" + provider, SessionID: "sess-" + provider,
			ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now,
			Source:  model.MemorySource{Kind: "runtime", Reference: "sess-" + provider},
			ExtMeta: map[string]any{"provider": provider},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	h := &CommandHandler{ws: ws}
	for _, configured := range []injectChannel{injectAuto, injectSystemPrompt} {
		if err := saveInjectChannel(ws.workDir, configured); err != nil {
			t.Fatal(err)
		}
		for _, provider := range []string{"claude", "codex", "opencode", "agy", "antigravity"} {
			t.Run(string(configured)+"/"+provider, func(t *testing.T) {
				canonical := provider
				if canonical == "agy" {
					canonical = "antigravity"
				}
				got, err := h.handleMemoryInject(ctx, []string{"preview", provider})
				resolved, note := resolveInjectChannel(canonical, configured)
				wantHeader := "BRIEFING FOR " + strings.ToUpper(canonical) + " via " + string(resolved) + " ("
				if err != nil || !strings.HasPrefix(got, wantHeader) {
					t.Fatalf("preview = %q, %v", got, err)
				}
				if note != "" && !strings.Contains(got, note) {
					t.Errorf("missing fallback note %q", note)
				}
				for _, other := range []string{"claude", "codex", "opencode", "antigravity"} {
					if strings.Contains(got, "Completed work from "+other) != (other != canonical) {
						t.Errorf("incorrect inclusion of %s: %s", other, got)
					}
				}
			})
		}
	}
}
