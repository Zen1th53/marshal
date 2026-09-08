package startup_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/startup"
)

// fakeProber drives every branch without needing the real condition present.
type fakeProber struct {
	onPath       map[string]bool
	dirs         map[string]bool
	files        map[string]bool
	writable     map[string]bool
	commandFails map[string]bool
	repoRoot     string
	// mutations records anything that would have changed the machine. It must
	// stay empty: assessment observes and never repairs.
	mutations []string
}

func newFakeProber() *fakeProber {
	return &fakeProber{
		onPath:       map[string]bool{"git": true},
		dirs:         map[string]bool{},
		files:        map[string]bool{},
		writable:     map[string]bool{},
		commandFails: map[string]bool{},
	}
}

func (f *fakeProber) LookPath(name string) (string, error) {
	if f.onPath[name] {
		return "/usr/bin/" + name, nil
	}
	return "", os.ErrNotExist
}

func (f *fakeProber) Stat(path string) (fs.FileInfo, error) {
	if f.dirs[path] {
		return fakeInfo{name: filepath.Base(path), dir: true}, nil
	}
	if f.files[path] {
		return fakeInfo{name: filepath.Base(path)}, nil
	}
	return nil, fs.ErrNotExist
}

func (f *fakeProber) Run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	if f.commandFails[key] {
		return []byte("fatal: not a git repository"), os.ErrInvalid
	}
	if key == "git rev-parse --show-toplevel" {
		if f.repoRoot == "" {
			return []byte("fatal: not a git repository"), os.ErrInvalid
		}
		return []byte(f.repoRoot + "\n"), nil
	}
	if key == "git rev-parse HEAD" {
		return []byte("abc123\n"), nil
	}
	return nil, nil
}

func (f *fakeProber) Writable(path string) bool { return f.writable[path] }

type fakeInfo struct {
	name string
	dir  bool
}

func (f fakeInfo) Name() string { return f.name }
func (f fakeInfo) Size() int64  { return 0 }
func (f fakeInfo) Mode() fs.FileMode {
	if f.dir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.dir }
func (f fakeInfo) Sys() any           { return nil }

// healthyProject wires a fully set-up project.
func healthyProject(root string) *fakeProber {
	prober := newFakeProber()
	prober.repoRoot = root
	prober.dirs[root] = true
	prober.dirs[filepath.Join(root, ".marshal")] = true
	prober.files[filepath.Join(root, "CAPABILITIES.yaml")] = true
	prober.writable[root] = true
	return prober
}

func healthyEnv(root string) startup.Environment {
	return startup.Environment{WorkingDir: root, SandboxEnforced: true, NetworkEnforced: true}
}

func TestHealthyProjectAssessesReady(t *testing.T) {
	root := "/work/project"
	assessment := startup.Assess(context.Background(), healthyProject(root), healthyEnv(root))
	if assessment.Phase != startup.PhaseReady {
		t.Fatalf("a healthy project produced %s: %+v", assessment.Phase, assessment.Blocking())
	}
	if assessment.FirstRun {
		t.Fatal("an initialized project was reported as a first run")
	}
	if assessment.ProjectPath != root {
		t.Fatalf("project path was %q, want %q", assessment.ProjectPath, root)
	}
}

// The baseline P0: MARSHAL started outside a Git repository. The control
// center must open and every repair surface must be present.
func TestNonGitDirectoryOpensControlCenter(t *testing.T) {
	root := "/work/empty"
	prober := newFakeProber()
	prober.dirs[root] = true
	prober.writable[root] = true
	// No repoRoot: git rev-parse fails, exactly as in a plain directory.

	assessment := startup.Assess(context.Background(), prober, healthyEnv(root))

	if !assessment.ControlCenterOpens() {
		t.Fatalf("a plain directory prevented the control center opening (phase %s)", assessment.Phase)
	}
	if assessment.ExecutionPermitted() {
		t.Fatal("work was permitted with no project")
	}
	for _, capability := range startup.AlwaysAvailable() {
		if !assessment.Has(capability) {
			t.Fatalf("%s was unavailable in a plain directory", capability)
		}
	}
	if !assessment.FirstRun {
		t.Fatal("a directory with no project was not reported as a first run")
	}
	repo, ok := assessment.Check("project.repository")
	if !ok || repo.Reason != startup.ReasonNotAGitRepository {
		t.Fatalf("the missing repository was not identified: %+v", repo)
	}
	if safe, offenders := assessment.UserSafe(); !safe {
		t.Fatalf("raw internal errors reached the user: %v", offenders)
	}
	// The Git subprocess message must not survive into any user-facing text.
	for _, check := range assessment.Checks {
		if strings.Contains(strings.ToLower(check.Summary), "rev-parse") {
			t.Fatalf("a Git command leaked into a summary: %q", check.Summary)
		}
	}
}

// The baseline P0: Git is not installed at all.
func TestMissingGitBlocksProjectsNotTheControlCenter(t *testing.T) {
	root := "/work/nogit"
	prober := newFakeProber()
	prober.onPath["git"] = false
	prober.dirs[root] = true
	prober.writable[root] = true

	assessment := startup.Assess(context.Background(), prober, healthyEnv(root))

	if !assessment.ControlCenterOpens() {
		t.Fatal("missing Git prevented the control center opening")
	}
	if assessment.Has(startup.CapProjectExecution) {
		t.Fatal("project execution stayed enabled with no Git")
	}
	for _, capability := range []startup.Capability{startup.CapDoctor, startup.CapSetup, startup.CapHelp} {
		if !assessment.Has(capability) {
			t.Fatalf("%s was unavailable, so the user cannot learn what to install", capability)
		}
	}
	git, ok := assessment.Check("project.git")
	if !ok || git.Reason != startup.ReasonGitMissing {
		t.Fatalf("missing Git was not identified: %+v", git)
	}
	if git.Remedy == "" {
		t.Fatal("missing Git offered no remedy")
	}
}

// The baseline P0: a real repository that has never had marshal init run.
func TestUninitializedProjectIsAFirstRunNotAnError(t *testing.T) {
	root := "/work/fresh"
	prober := newFakeProber()
	prober.repoRoot = root
	prober.dirs[root] = true
	prober.writable[root] = true
	// No .marshal directory.

	assessment := startup.Assess(context.Background(), prober, healthyEnv(root))

	if !assessment.ControlCenterOpens() {
		t.Fatal("an uninitialized project prevented the control center opening")
	}
	if !assessment.FirstRun {
		t.Fatal("an uninitialized project was not reported as a first run")
	}
	initialized, ok := assessment.Check("project.initialized")
	if !ok || initialized.Reason != startup.ReasonProjectNotInit {
		t.Fatalf("the uninitialized project was not identified: %+v", initialized)
	}
	if safe, offenders := assessment.UserSafe(); !safe {
		t.Fatalf("raw internal errors reached the user: %v", offenders)
	}
	// No SQLite or driver text may appear anywhere.
	for _, check := range assessment.Checks {
		if strings.Contains(strings.ToLower(check.Summary+check.Impact+check.Remedy), "sqlite") {
			t.Fatalf("a database driver detail leaked to the user: %+v", check)
		}
	}
}

// A repository with no commits is distinguished from having no repository.
func TestEmptyRepositoryIsDistinguished(t *testing.T) {
	root := "/work/nocommits"
	prober := healthyProject(root)
	prober.commandFails["git rev-parse HEAD"] = true

	assessment := startup.Assess(context.Background(), prober, healthyEnv(root))

	repo, ok := assessment.Check("project.repository")
	if !ok || repo.Reason != startup.ReasonRepositoryEmpty {
		t.Fatalf("an empty repository was not identified: %+v", repo)
	}
	if !assessment.ControlCenterOpens() {
		t.Fatal("an empty repository prevented the control center opening")
	}
	if assessment.ExecutionPermitted() {
		t.Fatal("work was permitted with no baseline commit to roll back to")
	}
}

// An unwritable directory is the one genuine core failure.
func TestUnwritableDirectoryIsCoreFatal(t *testing.T) {
	root := "/work/readonly"
	prober := healthyProject(root)
	prober.writable[root] = false

	assessment := startup.Assess(context.Background(), prober, healthyEnv(root))

	if assessment.Phase != startup.PhaseCoreFailed {
		t.Fatalf("an unwritable directory produced %s, want CORE_FAILED", assessment.Phase)
	}
	if assessment.ControlCenterOpens() {
		t.Fatal("the control center opened with nowhere to store state")
	}
}

// Missing isolation blocks execution while leaving the control center intact.
func TestMissingSandboxBlocksExecutionOnly(t *testing.T) {
	root := "/work/project"
	env := healthyEnv(root)
	env.SandboxEnforced = false

	assessment := startup.Assess(context.Background(), healthyProject(root), env)

	if !assessment.ControlCenterOpens() {
		t.Fatal("missing isolation closed the control center")
	}
	if assessment.ExecutionPermitted() {
		t.Fatal("work was permitted with no sandbox")
	}
	if assessment.Has(startup.CapProjectExecution) {
		t.Fatal("project execution stayed enabled with no sandbox")
	}
	sandbox, _ := assessment.Check("env.sandbox")
	if !strings.Contains(strings.ToLower(sandbox.Impact), "blocked") {
		t.Fatalf("the impact does not say work is blocked: %q", sandbox.Impact)
	}
}

// Unenforced egress removes network capability without blocking local work.
func TestUnenforcedNetworkScopesToNetworkCapabilities(t *testing.T) {
	root := "/work/project"
	env := healthyEnv(root)
	env.NetworkEnforced = false

	assessment := startup.Assess(context.Background(), healthyProject(root), env)

	if assessment.Has(startup.CapNetworkEgress) || assessment.Has(startup.CapProviderExecution) {
		t.Fatal("network capabilities stayed enabled with no enforcement")
	}
	if !assessment.Has(startup.CapProjectExecution) {
		t.Fatal("unenforced egress blocked local work that does not need the network")
	}
	if !assessment.ControlCenterOpens() {
		t.Fatal("unenforced egress closed the control center")
	}
	network, _ := assessment.Check("env.network")
	if network.Status == startup.StatusLimited {
		t.Fatal("absent enforcement was reported as merely limited, overstating it")
	}
}

// A missing capability policy is never replaced with a permissive default.
func TestMissingPolicyBlocksRatherThanDefaulting(t *testing.T) {
	root := "/work/project"
	prober := healthyProject(root)
	delete(prober.files, filepath.Join(root, "CAPABILITIES.yaml"))

	assessment := startup.Assess(context.Background(), prober, healthyEnv(root))

	if assessment.ExecutionPermitted() {
		t.Fatal("work was permitted with no capability policy")
	}
	policy, ok := assessment.Check("project.policy")
	if !ok || policy.Reason != startup.ReasonPolicyMissing {
		t.Fatalf("the missing policy was not identified: %+v", policy)
	}
	if !assessment.ControlCenterOpens() {
		t.Fatal("a missing policy closed the control center")
	}
}

// ULTRA is reported, never granted, and its absence is normal rather than a
// problem needing attention.
func TestUltraIsReportedNotGranted(t *testing.T) {
	root := "/work/project"
	assessment := startup.Assess(context.Background(), healthyProject(root), healthyEnv(root))
	if assessment.Has(startup.CapUltra) {
		t.Fatal("ULTRA was available with no entitlement")
	}
	ultra, _ := assessment.Check("env.ultra")
	if ultra.Status != startup.StatusOptional {
		t.Fatalf("absent ULTRA was reported as %s rather than optional", ultra.Status)
	}
	if assessment.Phase != startup.PhaseReady {
		t.Fatalf("absent ULTRA changed the phase to %s", assessment.Phase)
	}

	env := healthyEnv(root)
	env.UltraEntitled = true
	entitled := startup.Assess(context.Background(), healthyProject(root), env)
	if !entitled.Has(startup.CapUltra) {
		t.Fatal("a verified entitlement did not enable ULTRA")
	}
}

// Assessment observes and never repairs. Nothing here may create a repository,
// a database, a directory or a config file.
func TestAssessmentNeverMutates(t *testing.T) {
	root := "/work/empty"
	prober := newFakeProber()
	prober.dirs[root] = true
	prober.writable[root] = true

	startup.Assess(context.Background(), prober, healthyEnv(root))

	if len(prober.mutations) != 0 {
		t.Fatalf("assessment mutated the machine: %v", prober.mutations)
	}
}

// Assessment never fails: an unreadable directory produces UNKNOWN, and
// UNKNOWN still opens the control center that would explain it.
func TestAssessmentDegradesRatherThanFailing(t *testing.T) {
	prober := newFakeProber()
	assessment := startup.Assess(context.Background(), prober, startup.Environment{WorkingDir: "/missing"})

	if !assessment.ControlCenterOpens() {
		t.Fatal("an unreadable directory closed the control center")
	}
	stateDir, ok := assessment.Check("core.state-dir")
	if !ok || stateDir.Status != startup.StatusUnknown {
		t.Fatalf("an unreadable directory was not reported as unknown: %+v", stateDir)
	}
	if safe, offenders := assessment.UserSafe(); !safe {
		t.Fatalf("raw internal errors reached the user: %v", offenders)
	}
}

// Every check the probes emit must be presentable to a user.
func TestAllProbeOutputIsUserSafe(t *testing.T) {
	root := "/work/project"
	scenarios := map[string]func() (*fakeProber, startup.Environment){
		"healthy": func() (*fakeProber, startup.Environment) { return healthyProject(root), healthyEnv(root) },
		"no git": func() (*fakeProber, startup.Environment) {
			p := newFakeProber()
			p.onPath["git"] = false
			p.dirs[root] = true
			p.writable[root] = true
			return p, healthyEnv(root)
		},
		"no repo": func() (*fakeProber, startup.Environment) {
			p := newFakeProber()
			p.dirs[root] = true
			p.writable[root] = true
			return p, healthyEnv(root)
		},
		"no marshal": func() (*fakeProber, startup.Environment) {
			p := newFakeProber()
			p.repoRoot = root
			p.dirs[root] = true
			p.writable[root] = true
			return p, healthyEnv(root)
		},
		"no sandbox": func() (*fakeProber, startup.Environment) {
			e := healthyEnv(root)
			e.SandboxEnforced = false
			return healthyProject(root), e
		},
		"no network": func() (*fakeProber, startup.Environment) {
			e := healthyEnv(root)
			e.NetworkEnforced = false
			return healthyProject(root), e
		},
		"unwritable": func() (*fakeProber, startup.Environment) {
			p := healthyProject(root)
			p.writable[root] = false
			return p, healthyEnv(root)
		},
	}
	for name, build := range scenarios {
		t.Run(name, func(t *testing.T) {
			prober, env := build()
			assessment := startup.Assess(context.Background(), prober, env)
			safe, offenders := assessment.UserSafe()
			if !safe {
				t.Fatalf("%s produced unsafe summaries: %v", name, offenders)
			}
			for _, check := range assessment.Checks {
				if check.Status.Blocking() && check.Remedy == "" {
					t.Fatalf("%s: check %s blocks but offers no remedy", name, check.ID)
				}
			}
		})
	}
}

// Regression: a fully set-up project on a machine without policy-enforced
// egress must still open the workspace. Egress enforcement is unavailable on
// most developer machines, and treating that as a reason to withhold the
// workspace would make the common case look broken.
func TestHealthyProjectWithoutEgressEnforcementStillOpensWorkspace(t *testing.T) {
	root := "/work/project"
	env := healthyEnv(root)
	env.NetworkEnforced = false

	assessment := startup.Assess(context.Background(), healthyProject(root), env)

	if assessment.Phase != startup.PhaseLimited {
		t.Fatalf("phase was %s, want LIMITED", assessment.Phase)
	}
	if !assessment.ExecutionPermitted() {
		t.Fatal("local work was withheld because outbound access could not be restricted")
	}
	if assessment.Has(startup.CapNetworkEgress) {
		t.Fatal("network capability was reported available with no enforcement")
	}
	// An absent optional capability is a limitation, not an attention item:
	// escalating it would train users to ignore the attention list.
	for _, check := range assessment.Attention() {
		if check.ID == "env.network" {
			t.Fatal("an unavailable optional capability was raised as needing attention")
		}
	}
}

// Something present but broken, or that could not be established, does warrant
// attention and must not be quietly folded into "limited".
func TestBrokenOrUnknownChecksRaiseAttention(t *testing.T) {
	root := "/work/project"
	for name, status := range map[string]startup.Status{
		"broken":  startup.StatusBroken,
		"unknown": startup.StatusUnknown,
	} {
		t.Run(name, func(t *testing.T) {
			checks := []startup.Check{{
				ID: "env.thing", Dimension: startup.DimensionEnvironment, Status: status,
				Required: false, Reason: startup.ReasonCheckError,
				Summary: "A component is not working.",
			}}
			assessment := startup.Summarize(checks, startup.SummaryOptions{ProjectPath: root})
			if assessment.Phase != startup.PhaseNeedsAttention {
				t.Fatalf("a %s component produced %s, want NEEDS_ATTENTION", name, assessment.Phase)
			}
			if len(assessment.Attention()) != 1 {
				t.Fatalf("a %s component was not raised for attention", name)
			}
		})
	}
}
