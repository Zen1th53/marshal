//go:build linux

package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/bundle"
	"github.com/Zen1th53/marshal/internal/learning"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/optimization"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestCommandSweepConfigMalformed(t *testing.T) {
	sweepWorkEnvironment(t)
	_, stored, _ := newControlWorkspace(t)
	stored.workDir = t.TempDir()
	absent := NewWorkspace(nil, "sweep", "sweep")
	absent.workDir = t.TempDir()
	lines := []string{
		"/policy typo", "/policy network extra", "/sandbox typo", "/sandbox read-only extra",
		"/doctor typo", "/doctor codex extra", "/provider status extra", "/provider typo", "/provider config",
		"/harness typo", "/harness probe extra", "/harness status extra", "/harness select", "/harness select qa", "/harness select qa codex extra",
		"/model show extra", "/model select", "/model select codex", "/model select codex slug extra", "/model slug extra", "/models extra",
		"/effort typo", "/effort high extra", "/backup typo", "/backup create extra", "/backup restore", "/backup restore missing extra",
		"/fingerprint extra", "/runtime extra", "/store extra", "/export extra", "/blind typo", "/reinjection extra",
		"/alignment typo", "/alignment scope extra", "/optimization", "/optimization id extra",
		"/features typo", "/features list extra", "/features enable", "/features disable", "/features enable flag extra",
		"/search typo", "/search on extra", "/login extra", "/logout extra",
	}
	for _, ws := range []*Workspace{stored, absent} {
		for _, line := range lines {
			t.Run(line, func(t *testing.T) { sweepWorkRun(t, ws, line, "Usage:") })
		}
	}
	if files, _ := filepath.Glob(filepath.Join(stored.workDir, ".marshal", "backups", "*.db")); len(files) != 0 {
		t.Fatalf("malformed backup wrote files: %v", files)
	}
}

func TestCommandSweepConfigDiscovery(t *testing.T) {
	sweepWorkEnvironment(t)
	ws := NewWorkspace(nil, "sweep", "sweep")
	help := sweepWorkRun(t, ws, "/help", "")
	for _, cmd := range []string{"/policy", "/sandbox", "/doctor", "/provider", "/harness", "/model", "/models", "/effort", "/backup", "/fingerprint", "/runtime", "/store", "/export", "/blind", "/reinjection", "/alignment", "/optimization", "/features", "/search", "/login", "/logout"} {
		if !strings.Contains(help, "  "+cmd+" ") || !ws.knownCommand(cmd) {
			t.Errorf("missing help/completion: %s", cmd)
		}
	}
	for cmd, subs := range map[string][]string{
		"/backup": {"create", "restore"}, "/model": {"show", "select"}, "/features": {"list", "enable", "disable"},
		"/effort": {"minimal", "low", "medium", "high", "xhigh", "default"}, "/doctor": {"codex", "provider"}, "/blind": {"resolve"},
	} {
		for _, sub := range subs {
			found := false
			for _, value := range ws.completer.ctx.Subcommands[cmd] {
				if value == sub {
					found = true
				}
			}
			if !found {
				t.Errorf("missing completion: %s %s", cmd, sub)
			}
		}
	}
	sweepWorkRun(t, ws, "/provider typo", "config <name>")
	sweepWorkRun(t, ws, "/providers", "PROVIDER / HARNESS STATUS")
	sweepWorkRun(t, ws, "/providers config codex", "UNAVAILABLE")
	sweepWorkRun(t, ws, "/providers status extra", "Usage:")
	sweepWorkRun(t, ws, "/providers typo", "Usage:")
}

func TestCommandSweepConfigStoreFailures(t *testing.T) {
	sweepWorkEnvironment(t)
	st, ws, ctx := newControlWorkspace(t)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"/model show", "/effort", "/store", "/export", "/optimization missing", "/backup create"} {
		out, err := ws.ExecuteCommand(ctx, line)
		if err == nil || !strings.Contains(err.Error(), "reopen the TUI") {
			t.Errorf("%s hid failure/hint: %q %v", line, out, err)
		}
	}
}

func TestCommandSweepConfigExportFreshState(t *testing.T) {
	sweepWorkEnvironment(t)
	st, ws, ctx := newControlWorkspace(t)
	ws.workDir = t.TempDir()
	sweepWorkRun(t, ws, "/export", "No active goal")
	out, _ := ws.ExecuteCommand(ctx, "/export")
	if strings.Contains(out, "/goal <outcome>") {
		t.Errorf("export recommends unavailable mutation: %s", out)
	}
	goal := model.GoalContract{ID: "goal-config", SessionID: ws.sessionID, DesiredOutcome: "Fresh export", Risk: model.R1, AuthoritySource: "test", UnderstandingState: model.GoalReady}
	if err := st.SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	sweepWorkRun(t, ws, "/export", "Evidence bundle written")
	files, _ := filepath.Glob(filepath.Join(ws.workDir, ".marshal", "evidence", "*.json"))
	if len(files) != 1 {
		t.Fatalf("export artifacts: %v", files)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["goal_id"] != goal.ID {
		t.Fatalf("wrong canonical goal: %s", data)
	}
	var artifact bundle.EvidenceBundle
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatal(err)
	}
	if err := artifact.Validate(); err != nil {
		t.Fatal(err)
	}
	digest, err := artifact.ComputeDigest()
	if err != nil || digest != artifact.BundleDigest {
		t.Fatalf("export digest: %q %v", digest, err)
	}
	// A runtime revision arriving after the first export must be observed by
	// the next command, rather than exporting the previous cached revision.
	goal, err = st.GetActiveGoalContract(ctx, ws.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	goal.Revision++
	goal.DesiredOutcome = "New revision"
	if err := st.SaveGoalContract(ctx, goal, goal.Revision-1); err != nil {
		t.Fatal(err)
	}
	out = sweepWorkRun(t, ws, "/export", "[rev 2]")
	if !strings.Contains(out, goal.ID) {
		t.Fatalf("wrong goal in export: %s", out)
	}
}

func TestCommandSweepConfigBackupArtifacts(t *testing.T) {
	sweepWorkEnvironment(t)
	_, ws, ctx := newControlWorkspace(t)
	ws.workDir = t.TempDir()
	// Two rapid invocations must retain two distinct verified snapshots.
	for i := 0; i < 2; i++ {
		sweepWorkRun(t, ws, "/backup create", "written and verified")
	}
	files, _ := filepath.Glob(filepath.Join(ws.workDir, ".marshal", "backups", "*.db"))
	if len(files) != 2 {
		t.Errorf("backup collision: %v", files)
	}
	for _, path := range files {
		if _, err := store.VerifyBackup(ctx, path, "", 0); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "backup with spaces.db")
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	sweepWorkRun(t, ws, `/backup restore "`+path+`"`, "marshal state restore")
}

func TestCommandSweepConfigDoctorNativePTY(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	logPath := filepath.Join(t.TempDir(), "argv")
	t.Setenv("MARSHAL_CONFIG_ARGV", logPath)
	script := `#!/bin/sh
printf '%s\n' "$@" > "$MARSHAL_CONFIG_ARGV"
printf 'CONFIG-DOCTOR-ARGV\n'
`
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	project := initProject(t, bin)
	for _, line := range []string{"/doctor codex", "/doctor provider"} {
		t.Run(line, func(t *testing.T) {
			s := startFrozenTUIInProject(t, 50, 180, bin, project, "tui")
			s.send(line)
			s.send("\x1b")
			s.send("\r")
			s.mustSee("CONFIG-DOCTOR-ARGV")
			s.mustSee("Codex exited.")
			data, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			argv := strings.Split(strings.TrimSpace(string(data)), "\n")
			// Native briefing may add flags; the command tail must be doctor alone.
			if len(argv) == 0 || argv[len(argv)-1] != "doctor" {
				t.Fatalf("alias forwarded as extra native argument: %q", argv)
			}
		})
	}
}

// Bare, valid, mistyped, and extra-argument cases run without entitlement,
// providers, a goal, a session record, or a Marshal run, with and without a store.
func TestCommandSweepConfigRootsAndSubcommands(t *testing.T) {
	sweepWorkEnvironment(t)
	_, stored, _ := newControlWorkspace(t)
	stored.workDir = t.TempDir()
	absent := NewWorkspace(nil, "sweep", "sweep")
	absent.workDir = t.TempDir()
	cases := []struct{ cmd, bare, valid, result string }{
		{"/policy", "NOT VERIFIED", "/policy network", "NOT VERIFIED"},
		{"/sandbox", "NOT VERIFIED", "/sandbox read-only", "authority unavailable"},
		{"/doctor", "SYSTEM DIAGNOSTICS", "/doctor codex", "authority unavailable"},
		{"/provider", "PROVIDER / HARNESS STATUS", "/provider config codex", "UNAVAILABLE"},
		{"/harness", "HARNESS CAPABILITY PROBE", "/harness select qa codex", "NOT applied"},
		{"/model", "", "/model select codex test-model", "NOT applied"},
		{"/models", "authority unavailable", "/models", "authority unavailable"},
		{"/effort", "", "/effort high", ""},
		{"/backup", "Usage:", "/backup create", ""},
		{"/fingerprint", "NOT_AVAILABLE", "/fingerprint", "NOT_AVAILABLE"},
		{"/runtime", "NOT VERIFIED", "/runtime", "NOT VERIFIED"},
		{"/store", "", "/store", ""},
		{"/export", "", "/export", ""},
		{"/blind", "NOT VERIFIED", "/blind resolve reason text", "NOT recorded"},
		{"/reinjection", "NOT VERIFIED", "/reinjection", "NOT VERIFIED"},
		{"/alignment", "NOT VERIFIED", "/alignment resolve reason text", "NOT resolved"},
		{"/optimization", "Usage:", "/optimization missing", ""},
		{"/features", "authority unavailable", "/features list", "authority unavailable"},
		{"/search", "authority unavailable", "/search on", "authority unavailable"},
		{"/login", "authority unavailable", "/login", "authority unavailable"},
		{"/logout", "authority unavailable", "/logout", "authority unavailable"},
	}
	for _, v := range []struct {
		name string
		ws   *Workspace
	}{{"store", stored}, {"nil-store", absent}} {
		t.Run(v.name, func(t *testing.T) {
			for _, c := range cases {
				t.Run(c.cmd, func(t *testing.T) {
					sweepWorkRun(t, v.ws, c.cmd, c.bare)
					sweepWorkRun(t, v.ws, c.valid, c.result)
					sweepWorkRun(t, v.ws, c.cmd+"typo", "Unknown command")
				})
			}
			for _, cmd := range []string{"/policy", "/provider", "/harness", "/model", "/effort", "/backup", "/blind", "/alignment", "/features", "/doctor", "/search", "/sandbox"} {
				for _, sub := range v.ws.completer.ctx.Subcommands[cmd] {
					t.Run(cmd+"/"+sub, func(t *testing.T) {
						line := cmd + " " + sub
						sweepWorkRun(t, v.ws, line, "") // no subcommand arguments
						valid := line
						switch line {
						case "/provider config":
							valid += " codex"
						case "/harness select":
							valid += " qa codex"
						case "/model select":
							valid += " codex test-model"
						case "/backup restore":
							valid += " missing.db"
						case "/features enable", "/features disable":
							valid += " test_feature"
						}
						out, err := v.ws.ExecuteCommand(context.Background(), valid)
						if err != nil {
							if !strings.Contains(err.Error(), "backup_path") {
								t.Fatal(err)
							}
						} else if out == "" {
							t.Fatalf("silent command: %s", valid)
						}
						malformed := valid + " extra unknown"
						if cmd == "/blind" || cmd == "/alignment" && sub == "resolve" { // reason syntax is a product decision, and still fails closed
							sweepWorkRun(t, v.ws, malformed, "NOT")
						} else if cmd == "/provider" && sub == "config" {
							sweepWorkRun(t, v.ws, malformed, "Refusing")
						} else {
							sweepWorkRun(t, v.ws, malformed, "Usage:")
						}
						if cmd == "/model" {
							sweepWorkRun(t, v.ws, line+"typo", "authority unavailable") // any single token is a Codex slug; decision
						} else {
							sweepWorkRun(t, v.ws, line+"typo", "Usage:")
						}
					})
				}
			}
		})
	}
	sweepWorkRun(t, stored, "/provider config no-such-provider", "/provider status")
	refusal := sweepWorkRun(t, stored, "/provider config codex SECRET-SWEEP-TEST", "Refusing")
	if strings.Contains(refusal, "SECRET-SWEEP-TEST") {
		t.Fatal("credential echoed")
	}
}

func TestCommandSweepConfigAuthority(t *testing.T) {
	sweepWorkEnvironment(t)
	_, ws, ctx := newControlWorkspace(t)
	ws.workDir = t.TempDir()
	source, auth := testControl(t)
	ws.AttachControlSource(source)
	sweepWorkRun(t, ws, "/models", "CODEX MODELS")
	sweepWorkRun(t, ws, "/model gpt-6-astra", "successfully switched")
	if auth.selectedCodexModel != "gpt-6-astra" {
		t.Fatal("model not applied")
	}
	for _, line := range []string{"/model other extra", "/models extra"} {
		sweepWorkRun(t, ws, line, "Usage:")
	}
	if auth.codexModelRevision != 1 {
		t.Fatal("malformed model changed preference")
	}
	for _, line := range []string{"/doctor codex", "/doctor provider"} {
		sweepWorkRun(t, ws, line, "CODEX DOCTOR DIAGNOSTICS")
	}
	for _, line := range []string{"/search", "/search on", "/search off", "/search enable", "/search disable", "/search true", "/search false"} {
		sweepWorkRun(t, ws, line, "")
	}
	for _, line := range []string{"/sandbox read-only", "/sandbox workspace-write", "/login", "/logout"} {
		sweepWorkRun(t, ws, line, "unchanged")
	}
	// A local CLI double records all governed feature operations; nothing executes
	// a real provider, reads operator credentials, or reaches a service.
	logPath := filepath.Join(t.TempDir(), "features-argv")
	t.Setenv("MARSHAL_CONFIG_ARGV", logPath)
	script := `#!/bin/sh
printf '%s\n' "$@" > "$MARSHAL_CONFIG_ARGV"
printf 'test_feature stable true\n'
`
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ line, argv string }{
		{"/features", "features\nlist\n"}, {"/features list", "features\nlist\n"},
		{"/features enable test_feature", "features\nenable\ntest_feature\n"}, {"/features disable test_feature", "features\ndisable\ntest_feature\n"},
	} {
		sweepWorkRun(t, ws, c.line, "")
		data, err := os.ReadFile(logPath)
		if err != nil || string(data) != c.argv {
			t.Fatalf("%s: %q %v", c.line, data, err)
		}
	}
	if err := os.Remove(filepath.Join(os.Getenv("PATH"), "codex")); err != nil {
		t.Fatal(err)
	}
	outMissing := sweepWorkRun(t, ws, "/features list", "not found on PATH")
	if !strings.Contains(outMissing, "Install Codex") {
		t.Errorf("missing CLI recovery hint: %s", outMissing)
	}
	out, err := ws.ExecuteCommand(ctx, "/optimization missing")
	if err != nil || !strings.Contains(out, "cycle_id") {
		t.Fatalf("unknown optimization missing hint: %q %v", out, err)
	}
}

func TestCommandSweepConfigPTY(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	project := initProject(t, bin)
	cases := []struct{ line, want string }{
		{"/policy network", "Policy enforcement status: NOT VERIFIED"}, {"/sandbox read-only", "Install Codex"},
		{"/doctor codex", "Install Codex"}, {"/provider status", "PROVIDER / HARNESS STATUS"},
		{"/harness probe", "HARNESS CAPABILITY PROBE"}, {"/model show", "SAVED MODEL PREFERENCES"},
		{"/models", "Check Codex installation"}, {"/effort high", "NOT applied"},
		{"/backup create", "Backup written and verified"}, {"/fingerprint", "NOT_AVAILABLE"},
		{"/runtime", "Execution state: NOT VERIFIED"}, {"/store", "STORE STATUS"},
		{"/export", "No active goal"}, {"/blind resolve reason", "NOT recorded"},
		{"/reinjection", "CONSTRAINT RE-INJECTION"}, {"/alignment scope", "ALIGNMENT GUARD: NOT VERIFIED"},
		{"/optimization missing", "not found"}, {"/features list", "Install Codex"},
		{"/search on", "Install Codex"}, {"/login", "Install Codex"}, {"/logout", "Install Codex"},
	}
	for _, c := range cases {
		t.Run(c.line, func(t *testing.T) {
			s := startFrozenTUIInProject(t, 50, 180, bin, project, "tui")
			before := len(s.output())
			s.send(c.line)
			s.send("\x1b")
			s.send("\r")
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if strings.Contains(StripANSI(s.output()[before:]), c.want) {
					return
				}
				time.Sleep(40 * time.Millisecond)
			}
			t.Fatalf("%s: want %q, terminal tail: %s", c.line, c.want, tail(s.output()[before:], 3000))
		})
	}
}

func TestCommandSweepConfigNativeArgumentsPTY(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	logPath := filepath.Join(t.TempDir(), "argv")
	t.Setenv("MARSHAL_CONFIG_ARGV", logPath)
	script := `#!/bin/sh
printf '%s\n' "$@" > "$MARSHAL_CONFIG_ARGV"
printf 'CONFIG-NATIVE-RAN\n'
`
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	project := initProject(t, bin)
	for _, c := range []struct{ line, tail string }{
		{"/features", "features\nlist"}, {"/features list", "features\nlist"}, {"/features enable test_flag", "features\nenable\ntest_flag"}, {"/features disable test_flag", "features\ndisable\ntest_flag"},
		{"/login", "login"}, {"/logout", "logout"}, {"/search on", "--search"}, {"/search enable", "--search"}, {"/search true", "--search"},
		{"/search off", "web_search=\"disabled\""}, {"/search disable", "web_search=\"disabled\""}, {"/search false", "web_search=\"disabled\""},
		{"/sandbox read-only", "--sandbox\nread-only"}, {"/sandbox workspace-write", "--sandbox\nworkspace-write"},
	} {
		t.Run(c.line, func(t *testing.T) {
			if err := os.Remove(logPath); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			s := startFrozenTUIInProject(t, 50, 180, bin, project, "tui")
			s.send(c.line)
			s.send("\x1b")
			s.send("\r")
			s.mustSee("CONFIG-NATIVE-RAN")
			s.mustSee("Codex exited.")
			data, err := os.ReadFile(logPath)
			if err != nil || !strings.HasSuffix(strings.TrimSpace(string(data)), c.tail) {
				t.Fatalf("%s argv: %q %v", c.line, data, err)
			}
		})
	}
}

func TestCommandSweepConfigSavedPreferences(t *testing.T) {
	sweepWorkEnvironment(t)
	st, ws, ctx := newControlWorkspace(t)
	ws.workDir = t.TempDir()
	out := sweepWorkRun(t, ws, "/effort", "")
	if strings.Contains(out, "Saved reasoning preference") {
		t.Errorf("advisory fallback reported as saved: %s", out)
	}
	if err := st.SaveHarnessProfile(ctx, model.HarnessProfile{Harness: "codex", InstalledVersion: "test", BinaryPath: "/tmp/test-double", DefaultModel: "test-model", ReasoningKnobs: []string{"high"}, ProbeEvidenceID: "test-probe", ProbedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	sweepWorkRun(t, ws, "/model show", "test-model")
	sweepWorkRun(t, ws, "/effort", "Probed reasoning knobs: high")
	for _, line := range []string{"/model select codex replacement", "/harness select qa codex", "/effort low"} {
		sweepWorkRun(t, ws, line, "NOT applied")
	}
	profile, err := st.GetHarnessProfile(ctx, "codex")
	if err != nil || profile.DefaultModel != "test-model" || profile.ReasoningKnobs[0] != "high" {
		t.Fatalf("refused mutation changed profile: %+v %v", profile, err)
	}
	ws.router = nil
	sweepWorkRun(t, ws, "/effort high", "NOT applied")
	out, err = ws.ExecuteCommand(ctx, "/effort")
	if err == nil || !strings.Contains(err.Error(), "advisory router") {
		t.Errorf("missing router hint: %q %v", out, err)
	}
}

func TestCommandSweepConfigOptimizationRecord(t *testing.T) {
	sweepWorkEnvironment(t)
	st, ws, ctx := newControlWorkspace(t)
	ws.workDir = t.TempDir()
	cycle, err := optimization.NewCycle(optimization.Cycle{
		ID: "cycle-config", Provenance: "test",
		Binding:             optimization.Entry{ProjectID: controlProjectID, MemoryCommitID: "memory-test", MemoryVersion: 1, MemoryDigest: "digest-test", SourceSHA: "sha-test", TreeDigest: "tree-test", EnvironmentHash: "env-test", Outcome: learning.OutcomeVerifiedComplete},
		Objectives:          []optimization.Objective{{Name: "verified_success", HigherIsBetter: true, Weight: 1}},
		BlockedOptimization: []string{"promotion requires runtime authorization"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendOptimizationCycle(ctx, cycle); err != nil {
		t.Fatal(err)
	}
	out := sweepWorkRun(t, ws, "/optimization cycle-config", "OPTIMIZATION CYCLE cycle-config v1")
	if !strings.Contains(out, cycle.Digest) || !strings.Contains(out, cycle.BlockedOptimization[0]) {
		t.Fatalf("incomplete cycle evidence: %s", out)
	}
	sweepWorkRun(t, ws, "/optimization cycle-config extra", "Usage:")
	after, err := st.GetOptimizationCycle(ctx, cycle.ID)
	if err != nil || after.Digest != cycle.Digest {
		t.Fatalf("inspection changed cycle: %+v %v", after, err)
	}
}

func TestCommandSweepConfigDegradedHints(t *testing.T) {
	sweepWorkEnvironment(t)
	ws := NewWorkspace(nil, "sweep", "sweep")
	for _, line := range []string{"/models", "/model test-model", "/features", "/search", "/sandbox read-only", "/login", "/logout"} {
		t.Run(line, func(t *testing.T) { sweepWorkRun(t, ws, line, "Open the TUI") })
	}
	t.Run("optimization", func(t *testing.T) { sweepWorkRun(t, ws, "/optimization missing", "marshal init") })
	t.Run("doctor", func(t *testing.T) { sweepWorkRun(t, ws, "/doctor", "marshal init") })
	if !ws.knownCommand("/providers") {
		t.Error("accepted /providers alias missing from completion")
	}
}

func TestCommandSweepConfigEffortCapabilityIsNotSelection(t *testing.T) {
	sweepWorkEnvironment(t)
	st, ws, ctx := newControlWorkspace(t)
	ws.workDir = t.TempDir()
	if err := st.SaveHarnessProfile(ctx, model.HarnessProfile{Harness: "codex", InstalledVersion: "test", ReasoningKnobs: []string{"reasoning_effort", "high", "medium", "low"}, ProbedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	out := sweepWorkRun(t, ws, "/effort", "Selected effort: UNKNOWN")
	if strings.Contains(out, "Saved reasoning preference") {
		t.Fatalf("capability metadata presented as preference: %s", out)
	}
	if !strings.Contains(out, "Probed reasoning knobs: reasoning_effort, high, medium, low") {
		t.Fatalf("missing capability evidence: %s", out)
	}
}

func TestCommandSweepConfigArtifactFailures(t *testing.T) {
	sweepWorkEnvironment(t)
	st, ws, ctx := newControlWorkspace(t)
	ws.workDir = t.TempDir()
	if err := st.SaveGoalContract(ctx, model.GoalContract{ID: "goal-file-error", SessionID: ws.sessionID, DesiredOutcome: "Export fail closed", Risk: model.R1, AuthoritySource: "test", UnderstandingState: model.GoalReady}, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.workDir, ".marshal"), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"/backup create", "/export"} {
		out, err := ws.ExecuteCommand(ctx, line)
		if err == nil || !strings.Contains(err.Error(), "writable") || strings.Contains(out, "written") {
			t.Errorf("%s missing failure/hint: %q %v", line, out, err)
		}
	}
	bad := filepath.Join(t.TempDir(), "invalid-backup.db")
	if err := os.WriteFile(bad, []byte("not sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := ws.ExecuteCommand(ctx, "/backup restore "+bad); err == nil || !strings.Contains(err.Error(), "existing verified backup") || strings.Contains(out, "verified") {
		t.Fatalf("corrupt backup: %q %v", out, err)
	}
	for _, line := range []string{`/backup restore "unfinished`, `/backup restore ""`} {
		sweepWorkRun(t, ws, line, "Usage:")
	}
}

func TestCommandSweepConfigModelRepeatedSelection(t *testing.T) {
	sweepWorkEnvironment(t)
	_, ws, _ := newControlWorkspace(t)
	source, auth := testControl(t)
	ws.AttachControlSource(source)
	for _, slug := range []string{"gpt-6-astra", "gpt-5.6-terra", "gpt-6-astra"} {
		sweepWorkRun(t, ws, "/model "+slug, "successfully switched")
		if auth.selectedCodexModel != slug {
			t.Fatalf("selection not applied: %s", auth.selectedCodexModel)
		}
	}
	if auth.codexModelRevision != 3 {
		t.Fatalf("revision: %d", auth.codexModelRevision)
	}
}

func TestCommandSweepConfigSelectedModelNativePTY(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	logPath := filepath.Join(t.TempDir(), "argv")
	t.Setenv("MARSHAL_CONFIG_ARGV", logPath)
	script := `#!/bin/sh
printf '%s\n' "$@" > "$MARSHAL_CONFIG_ARGV"
printf 'CONFIG-SELECTED-MODEL-RAN\n'
`
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	root := initProject(t, bin)
	layout, err := project.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	projectRecord, err := st.Project(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetExecutionModelPreference(context.Background(), model.ExecutionModelPreference{ProjectID: projectRecord.ID, Adapter: "codex", Model: "sweep-preferred-model"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"/sandbox read-only", "/sandbox workspace-write", "/search on", "/search off"} {
		t.Run(line, func(t *testing.T) {
			s := startFrozenTUIInProject(t, 50, 180, bin, root, "tui")
			s.send(line)
			s.send("\x1b")
			s.send("\r")
			s.mustSee("CONFIG-SELECTED-MODEL-RAN")
			s.mustSee("Codex exited.")
			data, err := os.ReadFile(logPath)
			if err != nil || !strings.Contains(string(data), "--model\nsweep-preferred-model\n") {
				t.Fatalf("selected model dropped by %s: %q %v", line, data, err)
			}
		})
	}
}

func TestCommandSweepConfigProviderProbeOnly(t *testing.T) {
	sweepWorkEnvironment(t)
	ws := NewWorkspace(nil, "sweep", "sweep")
	logPath := filepath.Join(t.TempDir(), "unexpected-provider-run")
	t.Setenv("MARSHAL_CONFIG_ARGV", logPath)
	script := `#!/bin/sh
printf 'unexpected execution\n' > "$MARSHAL_CONFIG_ARGV"
exit 1
`
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	InvalidateProbeCache()
	out := sweepWorkRun(t, ws, "/provider status", "Auth:    UNKNOWN")
	if !strings.Contains(out, "BLOCKED_BY_POLICY") {
		t.Fatalf("changed egress observation: %s", out)
	}
	sweepWorkRun(t, ws, "/provider config codex", "Auth state: UNKNOWN")
	sweepWorkRun(t, ws, "/harness probe", "AVAILABLE")
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("probe executed provider: %v", err)
	}
}

func TestCommandSweepConfigModelRejectsFlags(t *testing.T) {
	sweepWorkEnvironment(t)
	_, ws, _ := newControlWorkspace(t)
	source, auth := testControl(t)
	ws.AttachControlSource(source)
	for _, arg := range []string{"--dangerously-bypass-approvals-and-sandbox", "--unknown"} {
		out := sweepWorkRun(t, ws, "/model "+arg, "")
		if strings.Contains(out, "successfully switched") {
			t.Errorf("CLI flag accepted as model: %s", out)
		}
	}
	if auth.codexModelRevision != 0 {
		t.Errorf("invalid model mutated preference: %d", auth.codexModelRevision)
	}
}
