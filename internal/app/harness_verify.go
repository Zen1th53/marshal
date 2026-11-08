package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/sandbox"
	"github.com/Zen1th53/marshal/internal/worker"
)

const harnessProbeReply = "MARSHAL_PROBE_OK"

// errHarnessProbeReply marks a probe run that did not end in the exact reply,
// as distinct from one whose isolation evidence was wrong.
var errHarnessProbeReply = errors.New("harness probe did not produce the exact reply")

// checkHarnessProbeReply names the first run check a probe failed, so the
// recorded reason says which one: status, exit code, timeout, cancel,
// truncated output, or the reply itself.
func checkHarnessProbeReply(result adapter.Result) error {
	switch {
	case result.TimedOut:
		return fmt.Errorf("%w: run timed out", errHarnessProbeReply)
	case result.Cancelled:
		return fmt.Errorf("%w: run was cancelled", errHarnessProbeReply)
	case result.OutputTruncated:
		return fmt.Errorf("%w: output was truncated", errHarnessProbeReply)
	case result.ExitCode != 0:
		return fmt.Errorf("%w: exit code %d", errHarnessProbeReply, result.ExitCode)
	case result.Status != adapter.StatusSuccess:
		return fmt.Errorf("%w: run status %s", errHarnessProbeReply, result.Status)
	}
	if reply := strings.TrimSpace(result.FinalText); reply != harnessProbeReply {
		if len(reply) > 120 {
			reply = strings.ToValidUTF8(reply[:120], "") + "..."
		}
		return fmt.Errorf("%w: reply was %q", errHarnessProbeReply, reply)
	}
	return nil
}

func validateHarnessProbe(result adapter.Result) error {
	isolation := result.Isolation
	if err := checkHarnessProbeReply(result); err != nil {
		return err
	}
	if isolation.Level != model.IsolationBwrap || !isolation.Available || !isolation.Filesystem || !isolation.Process || !isolation.Network {
		return errors.New("harness probe did not run inside an enforced sandbox")
	}
	return nil
}

func validateHostHarnessProbe(result adapter.Result) error {
	isolation := result.Isolation
	if err := checkHarnessProbeReply(result); err != nil {
		return err
	}
	if isolation.Level != model.IsolationProcessOnly || !isolation.Available || !isolation.Process || isolation.Filesystem || isolation.Network {
		return errors.New("harness probe did not report host execution")
	}
	return nil
}

// VerifyMarshalWorker qualifies a governed provider with edition-specific
// process evidence.
// Native agy qualification uses the separate VerifyNativeAgyWorker entry point.
// Injected adapters cannot establish either contract.
func (r *Runtime) VerifyMarshalWorker(ctx context.Context, provider, modelName string) (model.HarnessProfile, error) {
	profile, err := r.verifyMarshalWorker(ctx, provider, modelName)
	if err != nil {
		r.recordHarnessVerifyFailure(context.WithoutCancel(ctx), provider, modelName, err)
	}
	return profile, err
}

// recordHarnessVerifyFailure leaves durable evidence of a failed verification,
// so a probe never fails without a recorded cause. It grants nothing.
func (r *Runtime) recordHarnessVerifyFailure(ctx context.Context, provider, modelName string, cause error) {
	if r == nil || r.store == nil || cause == nil {
		return
	}
	id, err := model.NewID("EVENT-HARNESS-")
	if err != nil {
		return
	}
	reason := cause.Error()
	if len(reason) > 1000 {
		reason = reason[:1000]
	}
	reason = strings.ToValidUTF8(reason, "?")
	_ = r.store.AppendEvent(ctx, nil, model.Event{ID: id, Type: "HARNESS_VERIFY_FAILED", Timestamp: time.Now().UTC(), ProjectID: r.ProjectID(), Data: map[string]any{"provider": provider, "model": modelName, "error": reason}})
}

func (r *Runtime) verifyMarshalWorker(ctx context.Context, provider, modelName string) (model.HarnessProfile, error) {
	var profile model.HarnessProfile
	provider = privateAGYCanonical(provider)
	if provider == "claude-code" {
		provider = "claude"
	}
	if provider != "opencode" && provider != "codex" && provider != "claude" && !(provider == "agy" && privateAGYEnabled()) {
		return profile, errors.New("sandboxed harness verification supports codex, claude and opencode")
	}
	var modelErr error
	modelName, modelErr = r.harnessProbeModel(ctx, provider, modelName)
	if modelErr != nil {
		return profile, modelErr
	}
	if r.adapters[provider] != nil {
		return profile, errors.New("injected adapters cannot establish harness governance")
	}
	ctx = markHarnessVerification(ctx)
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := privateHarnessProbePreflight(ctx, provider); err != nil {
		return profile, err
	}
	dir, err := os.MkdirTemp("", "marshal-harness-probe-")
	if err != nil {
		return profile, err
	}
	defer os.RemoveAll(dir)
	if _, err := gitMarshal(ctx, r.layout.Root, "worktree", "add", "--detach", dir, "HEAD"); err != nil {
		return profile, err
	}
	defer gitMarshal(context.Background(), r.layout.Root, "worktree", "remove", "--force", dir)
	if err := privateHarnessProbeWorktree(provider, dir); err != nil {
		return profile, err
	}
	// Remove and close the probe's private synthetic credential storage.
	defer func() {
		r.honeypotMu.Lock()
		trap := r.honeypots[dir]
		delete(r.honeypots, dir)
		r.honeypotMu.Unlock()
		if trap != nil {
			_ = trap.Close()
		}
	}()
	id, err := model.NewID("PROBE-")
	if err != nil {
		return profile, err
	}
	ctx = withBoundCredentialScope(ctx, r, boundCredentialScope{Project: string(r.admittedProjectIdentity), StorageProject: r.ProjectID(), Provider: provider, Model: modelName, Purpose: "probe", Probe: id})
	task := model.Task{ID: id, Title: "Reply exactly " + harnessProbeReply + ". Do not use tools or change files.", Risk: model.R1}
	socket, closeEgress, err := r.startProviderEgress(ctx, id, "", provider, modelName, task, "runtime", id, model.RoleDeveloper)
	if err != nil {
		return profile, err
	}
	defer closeEgress()
	client, grant, err := r.resolveAdapter(ctx, provider, task, dir, "runtime", true, modelName, socket)
	if err != nil {
		return profile, err
	}
	if grant != "" && r.capabilityBroker != nil {
		defer r.capabilityBroker.Revoke(context.Background(), capability.RevokeRequest{GrantID: grant, Actor: capability.SubjectID("runtime")})
	}
	probe, err := client.Probe(ctx)
	if err != nil {
		return profile, err
	}
	result, err := client.Run(ctx, adapter.Request{TaskID: id, Title: task.Title, Worktree: dir, Model: modelName, AllowedOperations: []string{"read"}})
	if err != nil {
		return profile, err
	}
	validate := validateHarnessProbe
	if sandbox.HostExecutionPreferred() {
		validate = validateHostHarnessProbe
	}
	if err := validate(result); err != nil {
		// The provider's own quota or rate-limit message is the real cause
		// an operator or Marshal needs; name it before the raw stderr. Only a
		// failed run can be a limit: wrong isolation evidence never is.
		if errors.Is(err, errHarnessProbeReply) {
			if limit := providerLimitReason(result.Stdout, result.Stderr); limit != "" {
				r.noteProviderLimit(provider, limit)
				return profile, fmt.Errorf("%w: provider limit: %s", err, limit)
			}
		}
		if stderr := strings.TrimSpace(string(result.Stderr)); stderr != "" {
			return profile, fmt.Errorf("%w: stderr: %s", err, stderr)
		}
		return profile, err
	}
	status, err := gitMarshal(ctx, dir, "status", "--porcelain")
	if err != nil || status != "" {
		return profile, errors.New("harness probe modified its worktree")
	}
	version := installedCLIVersion(ctx, provider)
	if version == "" || !sameProbedCLIVersion(provider, version, probe.Version) {
		return profile, errors.New("harness changed version during verification")
	}
	now := time.Now().UTC()
	if err := r.store.AppendEvent(ctx, nil, model.Event{ID: id, Type: "HARNESS_VERIFIED", Timestamp: now, ProjectID: r.ProjectID(), Data: map[string]any{"provider": provider, "version": version, "model": modelName, "reply": result.FinalText, "isolation": result.Isolation}}); err != nil {
		return profile, err
	}
	features := map[string]model.FeatureStatus{"headless": model.StatusNative, "sandbox": model.StatusEmulated, "network": model.StatusEmulated}
	if sandbox.HostExecutionPreferred() {
		features["sandbox"], features["network"] = model.StatusUnsupported, model.StatusUnsupported
		features["host-execution"] = model.StatusNative
	}
	profile = model.HarnessProfile{Harness: marshalHarnessName(provider), InstalledVersion: version, SupportedModels: []string{modelName}, DefaultModel: modelName, ProbeEvidenceID: id, ProbedAt: now, ExpiresAt: now.Add(24 * time.Hour), FeatureSupport: features}
	if existing, getErr := r.store.GetHarnessProfile(ctx, profile.Harness); getErr == nil {
		profile = mergeVerifiedModels(existing, profile, now)
	}
	r.clearProviderLimit(provider)
	return profile, r.store.SaveHarnessProfile(ctx, profile)
}

// Native agy uses CLI-owned authentication and permission controls. Verify the
// native print contract without claiming sandbox or network confinement.
// VerifyNativeAgyWorker verifies native capabilities without granting governed execution.
func (r *Runtime) VerifyNativeAgyWorker(ctx context.Context, modelName string) (model.HarnessProfile, error) {
	var profile model.HarnessProfile
	if r.adapters["agy"] != nil {
		return profile, errors.New("injected adapters cannot establish native harness capabilities")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	binary, err := project.FindBinary("agy")
	if err != nil {
		return profile, err
	}
	dir, err := os.MkdirTemp("", "marshal-native-agy-probe-")
	if err != nil {
		return profile, err
	}
	defer os.RemoveAll(dir)
	if _, err := gitMarshal(ctx, r.layout.Root, "worktree", "add", "--detach", dir, "HEAD"); err != nil {
		return profile, err
	}
	defer gitMarshal(context.Background(), r.layout.Root, "worktree", "remove", "--force", dir)
	modelName, err = r.harnessProbeModel(ctx, "agy", modelName)
	if err != nil {
		return profile, err
	}
	client := antigravity.New(binary, worker.New(90*time.Second, 2*time.Second, 8<<20))
	probe, err := client.Probe(ctx)
	if err != nil {
		return profile, err
	}
	id, err := model.NewID("NATIVE-PROBE-")
	if err != nil {
		return profile, err
	}
	result, err := client.Run(ctx, adapter.Request{TaskID: id, Worktree: dir, Model: modelName, Title: "Reply exactly " + harnessProbeReply + ". This is a native capability probe. Do not invoke tools, run startup routines, or change files."})
	if err != nil {
		return profile, err
	}
	if result.Status != adapter.StatusSuccess || result.ExitCode != 0 || result.TimedOut || result.Cancelled || result.OutputTruncated || result.SessionID == "" || strings.TrimSpace(result.FinalText) != harnessProbeReply {
		return profile, errors.New("native agy probe did not produce the exact successful reply")
	}
	status, err := gitMarshal(ctx, dir, "status", "--porcelain")
	if err != nil || status != "" {
		return profile, errors.New("native agy probe modified its worktree")
	}
	version := installedCLIVersion(ctx, "agy")
	if version == "" || version != probe.Version {
		return profile, errors.New("agy changed version during native verification")
	}
	now := time.Now().UTC()
	if err := r.store.AppendEvent(ctx, nil, model.Event{ID: id, Type: "NATIVE_HARNESS_VERIFIED", ProjectID: r.ProjectID(), Timestamp: now, Data: map[string]any{"provider": "agy", "version": version, "model": modelName, "reply": result.FinalText, "session_id": result.SessionID, "mode": "native", "sandbox_enforced": false, "network_enforced": false}}); err != nil {
		return profile, err
	}
	profile = model.HarnessProfile{Harness: "antigravity", InstalledVersion: version, BinaryPath: binary, SupportedModels: []string{modelName}, DefaultModel: modelName, ProbeEvidenceID: id, ProbedAt: now, ExpiresAt: now.Add(24 * time.Hour), FeatureSupport: map[string]model.FeatureStatus{"headless": model.StatusNative, "accept-edits": model.StatusNative, "sandbox": model.StatusUnsupported, "network": model.StatusUnsupported}}
	if existing, getErr := r.store.GetHarnessProfile(ctx, profile.Harness); getErr == nil {
		profile = mergeVerifiedModels(existing, profile, now)
	}
	return profile, r.store.SaveHarnessProfile(ctx, profile)
}

// Never retain an empty-model profile: automatic verification uses only the
// durable project selection, a previously probed identity or native discovery.
func (r *Runtime) harnessProbeModel(ctx context.Context, provider, modelName string) (string, error) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" && provider == "opencode" {
		modelName = strings.TrimSpace(os.Getenv("MARSHAL_OPENCODE_MODEL"))
	}
	if modelName == "" && (provider == "codex" || provider == "claude" || (provider == "agy" && privateAGYEnabled())) && r != nil && r.store != nil {
		preference, err := r.store.GetExecutionModelPreference(ctx, localProjectID, provider)
		if err != nil && !errors.Is(err, model.ErrNotFound) {
			return "", err
		}
		modelName = strings.TrimSpace(preference.Model)
	}
	if modelName == "" && r != nil && r.store != nil {
		// A previous exact probe of the installed CLI is useful for re-probing,
		// even when its governance TTL expired. It grants no execution here.
		profile, err := r.store.GetHarnessProfile(ctx, marshalHarnessName(provider))
		if err != nil && !errors.Is(err, model.ErrNotFound) {
			return "", err
		}
		if profile != nil && profile.ProbeEvidenceID != "" && slices.Contains(profile.SupportedModels, profile.DefaultModel) && profile.InstalledVersion == installedCLIVersion(ctx, provider) {
			modelName = strings.TrimSpace(profile.DefaultModel)
		}
		if modelName == "" && (provider == "codex" || provider == "claude") {
			binary, err := project.FindBinary(provider)
			if err != nil {
				return "", err
			}
			runner := worker.New(60*time.Second, 2*time.Second, 8<<20)
			if provider == "codex" {
				_, modelName, err = codex.DiscoverModels(ctx, binary, runner)
			} else {
				_, modelName, err = claude.DiscoverModels(ctx, binary, runner)
			}
			if err != nil {
				return "", err
			}
		}
	}
	if modelName == "" && provider == "agy" {
		// This is only a candidate for a real probe. It does not qualify a
		// worker or delegate model selection to the host CLI.
		modelName = antigravity.DefaultModel
	}
	if modelName == "" {
		return "", errors.New("harness verification requires an explicit model; installed provider reported no current model")
	}
	if err := validatePrivateAGYModel(provider, modelName); err != nil {
		return "", err
	}
	return modelName, nil
}
