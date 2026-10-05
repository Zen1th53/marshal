package tui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

func peerTestMsg(text string, at time.Time) importer.Message {
	return importer.Message{Role: "assistant", Content: text, Timestamp: at}
}

func formatSet(set []string) string {
	if len(set) == 0 {
		return "none"
	}
	s := append([]string(nil), set...)
	sort.Strings(s)
	return strings.Join(s, "+")
}

// TestPeersVisibilityMatrix tests that for all 4 agents (claude, codex, opencode, antigravity),
// every visibility set each viewer can have (all 8 subsets of the other three agents,
// for each of the 4 viewers = 32 configurations) works correctly.
// For each configuration, it:
//   - seeds channel entries produced by each agent with unique markers;
//   - builds the viewer's view through the same code path MARSHAL uses;
//   - asserts the viewer sees exactly the entries of the agents in its set,
//     never entries of agents outside it, and never its own entries.
func TestPeersVisibilityMatrix(t *testing.T) {
	ctx := context.Background()

	type testCase struct {
		viewer       string
		visible      []string
		commandAgent string
		commandArgs  string
		fullCmd      string
	}

	var cases []testCase

	for vIdx, viewer := range knownProviders {
		others := othersOf(viewer)
		// 8 subsets of the other 3 agents
		for mask := 0; mask < (1 << len(others)); mask++ {
			visible := subsetOf(others, mask)

			// Alternate viewer name: test "agy" alias as well as "antigravity"
			cmdAgent := viewer
			if viewer == "antigravity" && mask%2 == 1 {
				cmdAgent = "agy"
			}

			// Format authors list for command
			var cmdArgs string
			switch len(visible) {
			case 0:
				cmdArgs = "none"
			case len(others):
				// Test "all" for some viewers and explicit full list for others
				if vIdx%2 == 0 {
					cmdArgs = "all"
				} else {
					var list []string
					for _, a := range visible {
						if a == "antigravity" {
							list = append(list, "agy")
						} else {
							list = append(list, a)
						}
					}
					cmdArgs = strings.Join(list, ",")
				}
			default:
				// Test comma-separated and space-separated formatting, with aliases
				var list []string
				for _, a := range visible {
					if a == "antigravity" && mask%2 == 1 {
						list = append(list, "agy")
					} else {
						list = append(list, a)
					}
				}
				if mask%2 == 0 {
					cmdArgs = strings.Join(list, ",")
				} else {
					cmdArgs = strings.Join(list, " ")
				}
			}

			fullCmd := fmt.Sprintf("/memory peers %s %s", cmdAgent, cmdArgs)
			cases = append(cases, testCase{
				viewer:       viewer,
				visible:      visible,
				commandAgent: cmdAgent,
				commandArgs:  cmdArgs,
				fullCmd:      fullCmd,
			})
		}
	}

	if len(cases) != 32 {
		t.Fatalf("expected 32 visibility configurations, got %d", len(cases))
	}

	for _, tc := range cases {
		tc := tc
		name := fmt.Sprintf("viewer=%s/sees=%s/cmd=%s", tc.viewer, formatSet(tc.visible), tc.commandArgs)
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			ws := &Workspace{workDir: root, theme: NewTheme(ThemeNoColor, false, false)}
			h := NewCommandHandler(ws)

			s, err := openStream(root)
			if err != nil {
				t.Fatalf("openStream: %v", err)
			}

			// 1. Seed channel entries produced by each agent with unique markers
			markers := make(map[string]string)
			for i, agent := range knownProviders {
				marker := fmt.Sprintf("UNIQUE_PAYLOAD_%s_%d_SEQ", strings.ToUpper(agent), i*100+len(tc.visible))
				markers[agent] = marker
				added, err := s.append(agent, "sess-test", peerTestMsg(marker, baseTime.Add(time.Duration(i)*time.Second)))
				if err != nil || !added {
					t.Fatalf("seed stream for %s: added=%v err=%v", agent, added, err)
				}
			}

			// 2. Open viewer's inbox view through MARSHAL's code path
			view, err := openInboxView(root, tc.viewer, true)
			if err != nil {
				t.Fatalf("openInboxView: %v", err)
			}

			// 3. Execute the real command handler to configure peers visibility
			out, err := h.Handle(ctx, tc.fullCmd)
			if err != nil {
				t.Fatalf("h.Handle(%q): %v", tc.fullCmd, err)
			}
			if !strings.Contains(out, "SHARED CHANNEL") {
				t.Fatalf("h.Handle(%q) expected shared channel header, got:\n%s", tc.fullCmd, out)
			}

			// 4. Build/refresh view through the same code path MARSHAL uses (startup / ticker)
			if err := refreshInboxView(root, view, s); err != nil {
				t.Fatalf("refreshInboxView: %v", err)
			}

			// 5. Read the viewer's on-disk view file
			data, err := os.ReadFile(inboxPath(root, tc.viewer))
			if err != nil {
				t.Fatalf("read inbox file: %v", err)
			}
			content := string(data)

			// 6. Assertions:
			// Viewer NEVER sees its own entries
			if strings.Contains(content, markers[tc.viewer]) {
				t.Errorf("viewer %s saw its own entry %q in view", tc.viewer, markers[tc.viewer])
			}

			// Viewer sees EXACTLY entries of agents in its set
			for _, p := range tc.visible {
				if !strings.Contains(content, markers[p]) {
					t.Errorf("viewer %s (visible=%v) missing entry from %s: %q", tc.viewer, tc.visible, p, markers[p])
				}
			}

			// Viewer NEVER sees entries of agents outside its set
			for _, p := range knownProviders {
				if p == tc.viewer {
					continue
				}
				if !containsProvider(tc.visible, p) {
					if strings.Contains(content, markers[p]) {
						t.Errorf("viewer %s (visible=%v) leaked entry from %s: %q", tc.viewer, tc.visible, p, markers[p])
					}
				}
			}
		})
	}
}

// TestPeersVisibilityDynamicUpdate tests changing a viewer's set after entries exist:
// old entries are hidden or revealed accordingly.
func TestPeersVisibilityDynamicUpdate(t *testing.T) {
	ctx := context.Background()

	for _, viewer := range knownProviders {
		t.Run("viewer_"+viewer, func(t *testing.T) {
			root := t.TempDir()
			ws := &Workspace{workDir: root, theme: NewTheme(ThemeNoColor, false, false)}
			h := NewCommandHandler(ws)

			s, err := openStream(root)
			if err != nil {
				t.Fatalf("openStream: %v", err)
			}

			others := othersOf(viewer)
			first := others[0]
			second := others[1]
			third := others[2]

			// Seed channel entries for all 4 agents
			markers := make(map[string]string)
			for i, agent := range knownProviders {
				marker := fmt.Sprintf("DYNAMIC_PAYLOAD_%s_%s", strings.ToUpper(agent), strings.ToUpper(viewer))
				markers[agent] = marker
				if _, err := s.append(agent, "sess-dyn", peerTestMsg(marker, baseTime.Add(time.Duration(i)*time.Second))); err != nil {
					t.Fatalf("append %s: %v", agent, err)
				}
			}

			view, err := openInboxView(root, viewer, true)
			if err != nil {
				t.Fatalf("openInboxView: %v", err)
			}

			// Step 1: Set to see only 'first'
			if _, err := h.Handle(ctx, fmt.Sprintf("/memory peers %s %s", viewer, first)); err != nil {
				t.Fatalf("set %s: %v", first, err)
			}
			if err := refreshInboxView(root, view, s); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(inboxPath(root, viewer))
			content := string(data)
			if !strings.Contains(content, markers[first]) {
				t.Fatalf("step 1: %s should see %s", viewer, first)
			}
			if strings.Contains(content, markers[second]) || strings.Contains(content, markers[third]) {
				t.Fatalf("step 1: %s should not see %s or %s", viewer, second, third)
			}
			if strings.Contains(content, markers[viewer]) {
				t.Fatalf("step 1: %s saw own entry", viewer)
			}

			// Step 2: Change to see 'second' and 'third', hiding 'first'
			if _, err := h.Handle(ctx, fmt.Sprintf("/memory peers %s %s,%s", viewer, second, third)); err != nil {
				t.Fatalf("set %s,%s: %v", second, third, err)
			}
			if err := refreshInboxView(root, view, s); err != nil {
				t.Fatal(err)
			}
			data, _ = os.ReadFile(inboxPath(root, viewer))
			content = string(data)
			// 'first' must now be hidden
			if strings.Contains(content, markers[first]) {
				t.Errorf("step 2: %s was not hidden after restriction removed it", first)
			}
			// 'second' and 'third' must now be revealed
			if !strings.Contains(content, markers[second]) {
				t.Errorf("step 2: %s was not revealed after being added", second)
			}
			if !strings.Contains(content, markers[third]) {
				t.Errorf("step 2: %s was not revealed after being added", third)
			}
			if strings.Contains(content, markers[viewer]) {
				t.Errorf("step 2: %s saw own entry", viewer)
			}

			// Step 3: Change to 'none'
			if _, err := h.Handle(ctx, fmt.Sprintf("/memory peers %s none", viewer)); err != nil {
				t.Fatalf("set none: %v", err)
			}
			if err := refreshInboxView(root, view, s); err != nil {
				t.Fatal(err)
			}
			data, _ = os.ReadFile(inboxPath(root, viewer))
			content = string(data)
			for _, agent := range knownProviders {
				if strings.Contains(content, markers[agent]) {
					t.Errorf("step 3 (none): %s should not be visible", agent)
				}
			}

			// Step 4: Change to 'all'
			if _, err := h.Handle(ctx, fmt.Sprintf("/memory peers %s all", viewer)); err != nil {
				t.Fatalf("set all: %v", err)
			}
			if err := refreshInboxView(root, view, s); err != nil {
				t.Fatal(err)
			}
			data, _ = os.ReadFile(inboxPath(root, viewer))
			content = string(data)
			for _, other := range others {
				if !strings.Contains(content, markers[other]) {
					t.Errorf("step 4 (all): %s was not revealed", other)
				}
			}
			if strings.Contains(content, markers[viewer]) {
				t.Errorf("step 4 (all): %s saw own entry", viewer)
			}
		})
	}
}

// TestPeersParticipantsSetting tests that an agent that is not a participant contributes nothing new.
func TestPeersParticipantsSetting(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := &Workspace{workDir: root, theme: NewTheme(ThemeNoColor, false, false)}
	h := NewCommandHandler(ws)

	s, err := openStream(root)
	if err != nil {
		t.Fatalf("openStream: %v", err)
	}

	// Initial entries from all 4 agents
	markers := make(map[string]string)
	for i, agent := range knownProviders {
		marker := fmt.Sprintf("PARTICIPANT_INITIAL_%s", strings.ToUpper(agent))
		markers[agent] = marker
		if _, err := s.append(agent, "sess-init", peerTestMsg(marker, baseTime.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatalf("append %s: %v", agent, err)
		}
	}

	// Viewer claude is configured to see all
	if _, err := h.Handle(ctx, "/memory peers claude all"); err != nil {
		t.Fatal(err)
	}
	view, err := openInboxView(root, "claude", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := refreshInboxView(root, view, s); err != nil {
		t.Fatal(err)
	}

	// Restrict participants to claude and codex only
	if _, err := h.Handle(ctx, "/memory peers participants claude,codex"); err != nil {
		t.Fatalf("set participants: %v", err)
	}

	cfg, problems := loadChannelConfig(root)
	if len(problems) != 0 {
		t.Fatalf("load config problems: %v", problems)
	}
	if !cfg.joins("claude") || !cfg.joins("codex") {
		t.Fatal("claude or codex not joining as participants")
	}
	if cfg.joins("opencode") || cfg.joins("antigravity") {
		t.Fatal("opencode or antigravity joining when excluded from participants")
	}

	// An agent outside the participants list contributes nothing new to the channel:
	// MARSHAL's watcher checks channelCfg.joins(provider) before wiring consume -> publish.
	// We verify that joins reports false for non-participants:
	if cfg.joins("opencode") {
		t.Error("opencode reported as joining channel")
	}
	if cfg.joins("antigravity") {
		t.Error("antigravity reported as joining channel")
	}

	// In Claude's view, opencode and antigravity entries are withheld even though claude was set to 'all'
	if err := refreshInboxView(root, view, s); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(inboxPath(root, "claude"))
	content := string(data)
	if !strings.Contains(content, markers["codex"]) {
		t.Error("claude should see participant codex")
	}
	if strings.Contains(content, markers["opencode"]) {
		t.Error("claude saw non-participant opencode")
	}
	if strings.Contains(content, markers["antigravity"]) {
		t.Error("claude saw non-participant antigravity")
	}

	// If participants is reset to 'all', they contribute and are seen again
	if _, err := h.Handle(ctx, "/memory peers participants all"); err != nil {
		t.Fatal(err)
	}
	cfgAll, _ := loadChannelConfig(root)
	for _, p := range knownProviders {
		if !cfgAll.joins(p) {
			t.Errorf("agent %s does not join after participants all", p)
		}
	}
	if err := refreshInboxView(root, view, s); err != nil {
		t.Fatal(err)
	}
	dataAll, _ := os.ReadFile(inboxPath(root, "claude"))
	contentAll := string(dataAll)
	if !strings.Contains(contentAll, markers["opencode"]) || !strings.Contains(contentAll, markers["antigravity"]) {
		t.Error("opencode and antigravity not visible after participants restored to all")
	}

	// Testing invalid 'none' for participants
	out, err := h.Handle(ctx, "/memory peers participants none")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "an empty participant list means all agents") {
		t.Errorf("expected usage message for participants none, got:\n%s", out)
	}
}

// TestPeersAllAndNone explicitly tests 'all' and 'none' settings and error cases.
func TestPeersAllAndNone(t *testing.T) {
	ctx := context.Background()

	for _, viewer := range knownProviders {
		t.Run("viewer_"+viewer, func(t *testing.T) {
			root := t.TempDir()
			ws := &Workspace{workDir: root, theme: NewTheme(ThemeNoColor, false, false)}
			h := NewCommandHandler(ws)

			s, err := openStream(root)
			if err != nil {
				t.Fatal(err)
			}

			markers := make(map[string]string)
			for i, agent := range knownProviders {
				marker := fmt.Sprintf("ALL_NONE_ENTRY_%s", strings.ToUpper(agent))
				markers[agent] = marker
				if _, err := s.append(agent, "sess-an", peerTestMsg(marker, baseTime.Add(time.Duration(i)*time.Second))); err != nil {
					t.Fatal(err)
				}
			}

			view, err := openInboxView(root, viewer, true)
			if err != nil {
				t.Fatal(err)
			}

			// 1. Test "all"
			if _, err := h.Handle(ctx, fmt.Sprintf("/memory peers %s all", viewer)); err != nil {
				t.Fatalf("set all: %v", err)
			}
			if err := refreshInboxView(root, view, s); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(inboxPath(root, viewer))
			content := string(data)
			for _, other := range othersOf(viewer) {
				if !strings.Contains(content, markers[other]) {
					t.Errorf("viewer %s (all) missing other agent %s", viewer, other)
				}
			}
			if strings.Contains(content, markers[viewer]) {
				t.Errorf("viewer %s (all) saw its own entry", viewer)
			}

			// 2. Test "none"
			if _, err := h.Handle(ctx, fmt.Sprintf("/memory peers %s none", viewer)); err != nil {
				t.Fatalf("set none: %v", err)
			}
			if err := refreshInboxView(root, view, s); err != nil {
				t.Fatal(err)
			}
			data, _ = os.ReadFile(inboxPath(root, viewer))
			content = string(data)
			for _, agent := range knownProviders {
				if strings.Contains(content, markers[agent]) {
					t.Errorf("viewer %s (none) saw entry from %s", viewer, agent)
				}
			}
		})
	}

	// 3. Test syntax validation
	t.Run("syntax_validation", func(t *testing.T) {
		root := t.TempDir()
		ws := &Workspace{workDir: root, theme: NewTheme(ThemeNoColor, false, false)}
		h := NewCommandHandler(ws)

		// none combined with another argument
		out, err := h.Handle(ctx, "/memory peers claude none codex")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "none must be used alone") {
			t.Errorf("expected 'none must be used alone', got: %s", out)
		}

		out, err = h.Handle(ctx, "/memory peers codex claude none")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "none must be used alone") {
			t.Errorf("expected 'none must be used alone', got: %s", out)
		}

		// participants none
		out, err = h.Handle(ctx, "/memory peers participants none")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "an empty participant list means all agents") {
			t.Errorf("expected empty participant list note, got: %s", out)
		}

		// participants all
		out, err = h.Handle(ctx, "/memory peers participants all")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "SHARED CHANNEL") {
			t.Errorf("expected shared channel output, got: %s", out)
		}
		cfg, _ := loadChannelConfig(root)
		for _, p := range knownProviders {
			if !cfg.joins(p) {
				t.Errorf("expected %s to join after participants all", p)
			}
		}
	})
}

// TestPeersViewerIndependence tests that settings for one viewer do not change another viewer's view.
func TestPeersViewerIndependence(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := &Workspace{workDir: root, theme: NewTheme(ThemeNoColor, false, false)}
	h := NewCommandHandler(ws)

	s, err := openStream(root)
	if err != nil {
		t.Fatalf("openStream: %v", err)
	}

	// Seed entries from all 4 agents
	markers := make(map[string]string)
	for i, agent := range knownProviders {
		marker := fmt.Sprintf("INDEP_MARKER_%s", strings.ToUpper(agent))
		markers[agent] = marker
		if _, err := s.append(agent, "sess-indep", peerTestMsg(marker, baseTime.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}

	// Open views for all 4 viewers
	views := make(map[string]*inboxView)
	for _, viewer := range knownProviders {
		v, err := openInboxView(root, viewer, true)
		if err != nil {
			t.Fatal(err)
		}
		views[viewer] = v
	}

	// Set initial configurations:
	// claude: codex
	// codex: opencode, antigravity (using agy alias)
	// opencode: none
	// antigravity: claude, codex
	if _, err := h.Handle(ctx, "/memory peers claude codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Handle(ctx, "/memory peers codex opencode,agy"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Handle(ctx, "/memory peers opencode none"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Handle(ctx, "/memory peers agy claude,codex"); err != nil {
		t.Fatal(err)
	}

	// Refresh and record codex view
	for _, viewer := range knownProviders {
		if err := refreshInboxView(root, views[viewer], s); err != nil {
			t.Fatal(err)
		}
	}

	codexExpected := func(content string) {
		t.Helper()
		if !strings.Contains(content, markers["opencode"]) || !strings.Contains(content, markers["antigravity"]) {
			t.Errorf("codex should see opencode and antigravity")
		}
		if strings.Contains(content, markers["claude"]) || strings.Contains(content, markers["codex"]) {
			t.Errorf("codex should not see claude or itself")
		}
	}

	codexData, _ := os.ReadFile(inboxPath(root, "codex"))
	codexExpected(string(codexData))

	// Now modify claude's settings repeatedly
	for _, change := range []string{
		"/memory peers claude none",
		"/memory peers claude all",
		"/memory peers claude opencode,agy",
	} {
		if _, err := h.Handle(ctx, change); err != nil {
			t.Fatalf("h.Handle(%q): %v", change, err)
		}
		// Check that codex's file is untouched in visibility
		codexCurrent, err := os.ReadFile(inboxPath(root, "codex"))
		if err != nil {
			t.Fatal(err)
		}
		codexExpected(string(codexCurrent))
	}

	// Now modify opencode's settings
	if _, err := h.Handle(ctx, "/memory peers opencode all"); err != nil {
		t.Fatal(err)
	}
	codexCurrent, _ := os.ReadFile(inboxPath(root, "codex"))
	codexExpected(string(codexCurrent))

	// Now modify antigravity's settings
	if _, err := h.Handle(ctx, "/memory peers agy none"); err != nil {
		t.Fatal(err)
	}
	codexCurrent, _ = os.ReadFile(inboxPath(root, "codex"))
	codexExpected(string(codexCurrent))
}
