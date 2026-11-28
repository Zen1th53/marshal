package startup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	providerspec "github.com/Zen1th53/marshal/internal/adapter/provider"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/store"
)

// ObserveReadiness performs only local, read-only inspection. Providers without
// a read-only auth command remain unknown until runtime evidence is supplied.
// It starts no provider process unless probeAuth is set (doctor
// --probe-providers): then codex's read-only `codex login status` is asked.
func ObserveReadiness(ctx context.Context, p Prober, root string, policy marshal.ReviewPolicy, probeAuth bool) marshal.Readiness {
	planner := ""
	path := filepath.Join(root, statepath.DirName, "default-provider")
	if root != "" && statepath.CheckPath(root, path) == nil {
		data, _ := os.ReadFile(path)
		planner = strings.TrimSpace(string(data))
	}
	providers := map[string]marshal.ProviderReadiness{}
	if root != "" && statepath.CheckPath(root, filepath.Join(root, statepath.DirName, "state.db")) == nil {
		if st, err := store.OpenReadOnly(ctx, filepath.Join(root, statepath.DirName, "state.db")); err == nil {
			defer st.Close()
			if project, err := st.Project(ctx); err == nil {
				if settings, err := st.GetMarshalSettings(ctx, project.ID); err == nil {
					policy = settings.Value.ReviewPolicy
				}
				if _, run, err := st.LatestMarshalRun(ctx, project.ID); err == nil && run.Value.State != marshal.Closed {
					policy = run.Value.EffectiveReviewPolicy()
				}
				if overview, err := st.ProviderOverview(ctx, project.ID, time.Now()); err == nil {
					for _, status := range overview {
						spec, ok := providerspec.ByHarness(status.Provider)
						if ok {
							signedIn := !status.VerifiedAt.IsZero() && !status.Expired
							reason := strings.ToLower(status.LastLimitReason)
							if status.LastLimitAt.After(status.VerifiedAt) && (strings.Contains(reason, "authentication required") || strings.Contains(reason, "not logged in") || strings.Contains(reason, "not signed in")) {
								signedIn = false
							}
							providers[spec.ID] = marshal.ProviderReadiness{SignedIn: signedIn}
							if status.LimitUntil.After(time.Now()) {
								f := providers[spec.ID]
								f.Unavailable = "provider limit; wait until " + status.LimitUntil.Format(time.RFC3339)
								providers[spec.ID] = f
							}
						}
					}
				}
			}
		}
	}
	for _, name := range []string{"claude", "codex", "agy", "opencode"} {
		binary, err := p.LookPath(name)
		fact := providers[name]
		fact.Installed = err == nil
		fact.AuthUnknown = fact.Installed && !fact.SignedIn
		if err == nil && name == "codex" && probeAuth {
			output, err := p.Run(ctx, root, binary, "login", "status")
			text := strings.ToLower(string(output))
			signedOut := strings.Contains(text, "not logged in") || strings.Contains(text, "not signed in") || strings.Contains(text, "authentication required")
			fact.SignedIn = err == nil && strings.Contains(text, "logged in") && !signedOut
			fact.AuthUnknown = !signedOut && !fact.SignedIn
		}
		providers[name] = fact
	}
	return marshal.ComputeReadiness(planner, providers, policy)
}

func readinessCheck(r marshal.Readiness) Check {
	c := Check{ID: "execution.readiness", Dimension: DimensionExecution, Required: true, Status: StatusReady, Summary: r.Guarantee, Capabilities: []Capability{CapProviderExecution}}
	if r.Blocker != "" {
		c.Status = StatusNeedsAttention
		c.Summary = r.Blocker
		c.Remedy = r.Blocker
	}
	return c
}
