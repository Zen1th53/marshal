package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
)

const harnessProbeReply = "MARSHAL_PROBE_OK"

func validateHarnessProbe(result adapter.Result) error {
	isolation := result.Isolation
	if result.Status != adapter.StatusSuccess || result.ExitCode != 0 || result.TimedOut || result.Cancelled || result.OutputTruncated || strings.TrimSpace(result.FinalText) != harnessProbeReply || isolation.Level != model.IsolationBwrap || !isolation.Available || !isolation.Filesystem || !isolation.Process || !isolation.Network {
		return errors.New("harness probe did not produce the exact reply inside an enforced sandbox")
	}
	return nil
}

// VerifyMarshalWorker records capability evidence only after a real adapter
// runs successfully under the runtime's sandbox, proxy and output guards.
// It never imports host credentials or accepts injected adapters as evidence.
func (r *Runtime) VerifyMarshalWorker(ctx context.Context, provider, modelName string) (model.HarnessProfile, error) {
	var profile model.HarnessProfile
	if provider != "opencode" {
		return profile, errors.New("sandboxed harness verification currently supports opencode")
	}
	if modelName == "" {
		modelName = strings.TrimSpace(os.Getenv("MARSHAL_OPENCODE_MODEL"))
	}
	if modelName == "" {
		return profile, errors.New("harness verification requires an explicit model")
	}
	if r.adapters[provider] != nil {
		return profile, errors.New("injected adapters cannot establish harness governance")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "marshal-harness-probe-")
	if err != nil {
		return profile, err
	}
	defer os.RemoveAll(dir)
	if _, err := gitMarshal(ctx, r.layout.Root, "worktree", "add", "--detach", dir, "HEAD"); err != nil {
		return profile, err
	}
	defer gitMarshal(context.Background(), r.layout.Root, "worktree", "remove", "--force", dir)
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
	if err := validateHarnessProbe(result); err != nil {
		return profile, fmt.Errorf("%w: %s", err, string(result.Stderr))
	}
	status, err := gitMarshal(ctx, dir, "status", "--porcelain")
	if err != nil || status != "" {
		return profile, errors.New("harness probe modified its worktree")
	}
	version := installedCLIVersion(ctx, provider)
	if version == "" || version != probe.Version {
		return profile, errors.New("harness changed version during verification")
	}
	now := time.Now().UTC()
	if err := r.store.AppendEvent(ctx, nil, model.Event{ID: id, Type: "HARNESS_VERIFIED", Timestamp: now, ProjectID: r.ProjectID(), Data: map[string]any{"provider": provider, "version": version, "model": modelName, "reply": result.FinalText, "isolation": result.Isolation}}); err != nil {
		return profile, err
	}
	profile = model.HarnessProfile{Harness: provider, InstalledVersion: version, SupportedModels: []string{modelName}, DefaultModel: modelName, ProbeEvidenceID: id, ProbedAt: now, ExpiresAt: now.Add(24 * time.Hour), FeatureSupport: map[string]model.FeatureStatus{"headless": model.StatusNative, "sandbox": model.StatusEmulated, "network": model.StatusEmulated}}
	return profile, r.store.SaveHarnessProfile(ctx, profile)
}
