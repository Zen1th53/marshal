package startup

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// This file observes the environment. Every probe here is read-only: it looks,
// it does not fix. That separation is the whole of the Setup/Doctor
// distinction — Setup assesses, Doctor diagnoses and may repair with explicit
// consent — and it is enforced structurally by giving assessment no code path
// that writes anything.
//
// In particular nothing here runs `git init`, creates a database, writes
// configuration, installs a dependency or enables an entitlement. A user who
// runs MARSHAL in the wrong directory gets an explanation, not a new
// repository.

// Prober observes the environment. It is an interface so that tests can drive
// every branch without needing the real condition present on the machine.
type Prober interface {
	// LookPath reports whether an executable is on PATH.
	LookPath(name string) (string, error)
	// Stat inspects a filesystem path.
	Stat(path string) (fs.FileInfo, error)
	// Run executes a read-only command and returns its combined output.
	Run(ctx context.Context, dir, name string, args ...string) ([]byte, error)
	// Writable reports whether a directory can be written to.
	Writable(path string) bool
}

// systemProber is the real implementation.
type systemProber struct{}

// NewSystemProber returns a prober that observes the real machine.
func NewSystemProber() Prober { return systemProber{} }

func (systemProber) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (systemProber) Stat(path string) (fs.FileInfo, error) { return os.Stat(path) }

func (systemProber) Run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	// Probes are bounded so that a hung external tool degrades one check
	// rather than preventing the control center from opening at all.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	// Probes never inherit stdin: a tool that decides to prompt would
	// otherwise hang startup waiting for input the user cannot see.
	cmd.Stdin = nil
	return cmd.CombinedOutput()
}

func (systemProber) Writable(path string) bool {
	probe := filepath.Join(path, ".marshal-write-probe")
	file, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return false
	}
	file.Close()
	os.Remove(probe)
	return true
}

// Environment is the input to assessment: where MARSHAL was started and what
// it is allowed to consider.
type Environment struct {
	// WorkingDir is the directory the user ran MARSHAL in.
	WorkingDir string
	// SandboxEnforced and NetworkEnforced report real isolation state, as
	// determined by the runtime rather than assumed by startup.
	SandboxEnforced bool
	NetworkEnforced bool
	// UltraEntitled reports a verified, unexpired entitlement.
	UltraEntitled bool
	// Now is the assessment clock.
	Now time.Time
}

// Assess observes the environment and returns the startup picture.
//
// It never returns an error. A probe that cannot run produces an UNKNOWN check
// rather than a failure, because the purpose of this function is to explain
// the situation, and a function that can itself fail to explain would
// reintroduce the very problem Process 01 exists to fix.
func Assess(ctx context.Context, prober Prober, env Environment) Assessment {
	started := time.Now()
	if prober == nil {
		prober = NewSystemProber()
	}
	now := env.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	var checks []Check
	checks = append(checks, probeStateDirectory(prober, env)...)
	gitChecks, projectRoot := probeGitAndProject(ctx, prober, env)
	checks = append(checks, gitChecks...)
	checks = append(checks, probeMarshalProject(prober, projectRoot)...)
	// Identity is checked with the same evidence the runtime uses to admit a
	// project, so readiness cannot report Ready for state the runtime would
	// refuse to open.
	if projectRoot != "" && marshalInitialized(prober, projectRoot) {
		checks = append(checks, ProjectIdentityCheck(ctx, projectRoot))
	}
	checks = append(checks, probeIsolation(env)...)
	checks = append(checks, probeUltra(env))

	firstRun := projectRoot == "" || !marshalInitialized(prober, projectRoot)

	assessment := Summarize(checks, SummaryOptions{
		FirstRun:      firstRun,
		ProjectPath:   projectRoot,
		UltraEntitled: env.UltraEntitled,
		Now:           now,
		Duration:      time.Since(started),
	})
	return assessment
}

// probeStateDirectory checks whether MARSHAL can hold its own state. This is
// the only dimension that can prevent the control center opening, so it is
// scoped narrowly: it asks whether the directory MARSHAL would write to is
// writable, and nothing more.
func probeStateDirectory(prober Prober, env Environment) []Check {
	dir := env.WorkingDir
	if dir == "" {
		// With no working directory there is nothing to check and nothing to
		// blame. UNKNOWN is the honest answer, and it does not close the
		// control center.
		return []Check{{
			ID: "core.state-dir", Dimension: DimensionCore, Status: StatusUnknown,
			Required: false, Reason: ReasonNotChecked,
			Summary: "MARSHAL could not determine which directory it started in.",
			Impact:  "Project state cannot be located until a directory is chosen.",
			Remedy:  Describe(ReasonNotChecked).Remedy,
		}}
	}
	if info, err := prober.Stat(dir); err != nil || !info.IsDir() {
		return []Check{{
			ID: "core.state-dir", Dimension: DimensionCore, Status: StatusUnknown,
			Required: false, Reason: ReasonNotChecked,
			Summary: "The current directory could not be read.",
			Impact:  "MARSHAL cannot tell what is here.",
			Remedy:  "Change to a directory you can read and start again.",
		}}
	}
	if !prober.Writable(dir) {
		// A directory MARSHAL cannot write to is a genuine core failure: it
		// has nowhere to keep the state its own control center depends on.
		return []Check{{
			ID: "core.state-dir", Dimension: DimensionCore, Status: StatusBroken,
			Required: true, Reason: ReasonStateDirUnwritable,
			Summary: "This directory cannot be written to.",
			Impact:  "MARSHAL cannot store project state here.",
			Remedy:  Describe(ReasonStateDirUnwritable).Remedy,
		}}
	}
	return []Check{{
		ID: "core.state-dir", Dimension: DimensionCore, Status: StatusReady,
		Required: true, Reason: ReasonOK,
		Summary: "MARSHAL can store state in this directory.",
	}}
}

// probeGitAndProject determines Git availability and whether the working
// directory sits inside a usable repository. It returns the repository root
// when one was found.
//
// Missing Git blocks project execution and nothing else. This is the single
// most important scoping decision in Process 01: a user without Git can still
// open MARSHAL, read Help, run Doctor and understand what to install.
func probeGitAndProject(ctx context.Context, prober Prober, env Environment) ([]Check, string) {
	if _, err := prober.LookPath("git"); err != nil {
		return []Check{{
			ID: "project.git", Dimension: DimensionProject, Status: StatusMissing,
			Required: true, Reason: ReasonGitMissing,
			Summary:      "Git is not installed.",
			Impact:       "Projects cannot be opened or worked on. MARSHAL itself still runs.",
			Remedy:       Describe(ReasonGitMissing).Remedy,
			Capabilities: []Capability{CapProjectExecution},
		}}, ""
	}

	output, err := prober.Run(ctx, env.WorkingDir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		// Not being in a repository is an ordinary situation, not an error
		// worth showing a subprocess message for.
		return []Check{{
			ID: "project.repository", Dimension: DimensionProject, Status: StatusMissing,
			Required: true, Reason: ReasonNotAGitRepository,
			Summary:      "This directory is not part of a Git repository.",
			Impact:       "There is no project here to work on yet.",
			Remedy:       Describe(ReasonNotAGitRepository).Remedy,
			Capabilities: []Capability{CapProjectExecution},
		}}, ""
	}
	root := strings.TrimSpace(string(output))

	checks := []Check{{
		ID: "project.git", Dimension: DimensionProject, Status: StatusReady,
		Required: true, Reason: ReasonOK,
		Summary: "Git is available.",
	}}

	// A repository with no commits is usable for setup but has no baseline to
	// work from, which is a different situation from having no repository.
	if _, headErr := prober.Run(ctx, root, "git", "rev-parse", "HEAD"); headErr != nil {
		checks = append(checks, Check{
			ID: "project.repository", Dimension: DimensionProject, Status: StatusNeedsAttention,
			Required: true, Reason: ReasonRepositoryEmpty,
			Summary:      "This repository has no commits yet.",
			Impact:       "There is no baseline to compare work against or roll back to.",
			Remedy:       Describe(ReasonRepositoryEmpty).Remedy,
			Capabilities: []Capability{CapProjectExecution},
		})
		return checks, root
	}

	checks = append(checks, Check{
		ID: "project.repository", Dimension: DimensionProject, Status: StatusReady,
		Required: true, Reason: ReasonOK,
		Summary: "A Git repository was found.",
	})
	return checks, root
}

// marshalInitialized reports whether MARSHAL has been set up in a project.
func marshalInitialized(prober Prober, root string) bool {
	if root == "" {
		return false
	}
	info, err := prober.Stat(filepath.Join(root, ".marshal"))
	return err == nil && info.IsDir()
}

// probeMarshalProject checks whether this project has been set up, and whether
// the capability policy it depends on is present and readable.
func probeMarshalProject(prober Prober, root string) []Check {
	if root == "" {
		return []Check{{
			ID: "project.initialized", Dimension: DimensionProject, Status: StatusMissing,
			Required: true, Reason: ReasonNoProject,
			Summary:      "No project is open.",
			Impact:       "Open or initialize a project to start work.",
			Remedy:       Describe(ReasonNoProject).Remedy,
			Capabilities: []Capability{CapProjectExecution},
		}}
	}

	var checks []Check
	if !marshalInitialized(prober, root) {
		// First run in a real repository. This is a normal, expected state and
		// is reported as something to do rather than something that failed.
		checks = append(checks, Check{
			ID: "project.initialized", Dimension: DimensionProject, Status: StatusMissing,
			Required: true, Reason: ReasonProjectNotInit,
			Summary:      "MARSHAL has not been set up for this project yet.",
			Impact:       "Work cannot run until the project is set up.",
			Remedy:       Describe(ReasonProjectNotInit).Remedy,
			Capabilities: []Capability{CapProjectExecution},
		})
		return checks
	}
	checks = append(checks, Check{
		ID: "project.initialized", Dimension: DimensionProject, Status: StatusReady,
		Required: true, Reason: ReasonOK,
		Summary: "This project is set up for MARSHAL.",
	})

	// The capability policy is required and is never replaced with a permissive
	// default when missing or unreadable: silently substituting a default would
	// turn a configuration mistake into an unnoticed loss of enforcement.
	policyPath := filepath.Join(root, "CAPABILITIES.yaml")
	if _, err := prober.Stat(policyPath); err != nil {
		status, reason := StatusMissing, ReasonPolicyMissing
		summary := "The project's capability policy file is missing."
		if !errors.Is(err, fs.ErrNotExist) {
			status, reason = StatusBroken, ReasonPolicyInvalid
			summary = "The project's capability policy file could not be read."
		}
		checks = append(checks, Check{
			ID: "project.policy", Dimension: DimensionProject, Status: status,
			Required: true, Reason: reason,
			Summary:      summary,
			Impact:       "Work cannot run without a policy defining what is permitted.",
			Remedy:       Describe(reason).Remedy,
			Capabilities: []Capability{CapProjectExecution},
		})
		return checks
	}
	checks = append(checks, Check{
		ID: "project.policy", Dimension: DimensionProject, Status: StatusReady,
		Required: true, Reason: ReasonOK,
		Summary: "The project's capability policy is present.",
	})
	return checks
}

// probeIsolation reports sandbox and network enforcement.
//
// Both are reported from what the runtime actually determined, never inferred
// from the presence of a binary. Neither is ever downgraded to "good enough":
// when isolation is unavailable, execution is blocked rather than allowed to
// proceed unprotected, which is the behaviour Process 00 requires.
func probeIsolation(env Environment) []Check {
	var checks []Check

	if env.SandboxEnforced {
		checks = append(checks, Check{
			ID: "env.sandbox", Dimension: DimensionEnvironment, Status: StatusReady,
			Required: true, Reason: ReasonOK,
			Summary: "Work runs in an isolated sandbox.",
		})
	} else {
		checks = append(checks, Check{
			ID: "env.sandbox", Dimension: DimensionEnvironment, Status: StatusMissing,
			Required: true, Reason: ReasonSandboxUnavailable,
			Summary:      "Sandbox isolation is not available.",
			Impact:       "Work is blocked rather than run without isolation.",
			Remedy:       Describe(ReasonSandboxUnavailable).Remedy,
			Capabilities: []Capability{CapProjectExecution, CapProviderExecution},
		})
	}

	if env.NetworkEnforced {
		checks = append(checks, Check{
			ID: "env.network", Dimension: DimensionEnvironment, Status: StatusReady,
			Required: false, Reason: ReasonOK,
			Summary: "Outbound access is limited to approved destinations.",
		})
	} else {
		// Unenforced egress removes network and provider capability but does
		// not block local work, so it is not marked Required. Reporting it as
		// LIMITED would overstate it: enforcement is absent, not partial.
		checks = append(checks, Check{
			ID: "env.network", Dimension: DimensionEnvironment, Status: StatusMissing,
			Required: false, Reason: ReasonNetworkUnenforced,
			Summary:      "Outbound access cannot be restricted to approved destinations.",
			Impact:       "Features that reach the network are unavailable. Local work continues.",
			Remedy:       Describe(ReasonNetworkUnenforced).Remedy,
			Capabilities: []Capability{CapNetworkEgress, CapProviderExecution},
		})
	}
	return checks
}

// probeUltra reports entitlement state. ULTRA is never enabled here; startup
// reports what an entitlement check found, and the capability follows from
// that rather than from any local flag.
func probeUltra(env Environment) Check {
	if env.UltraEntitled {
		return Check{
			ID: "env.ultra", Dimension: DimensionEnvironment, Status: StatusReady,
			Required: false, Reason: ReasonOK,
			Summary:      "ULTRA is available.",
			Capabilities: []Capability{CapUltra},
		}
	}
	return Check{
		ID: "env.ultra", Dimension: DimensionEnvironment, Status: StatusOptional,
		Required: false, Reason: ReasonUltraNotEntitled,
		Summary:      "ULTRA is not enabled.",
		Impact:       "Standard mode is fully available.",
		Remedy:       Describe(ReasonUltraNotEntitled).Remedy,
		Capabilities: []Capability{CapUltra},
	}
}
