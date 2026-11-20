// Package providers is the capability registry: what Marshal may ask of
// each provider CLI, implemented once per provider, so application and TUI
// code asks a capability instead of switching on a provider's name.
//
// Every answer comes from the installed CLI or from files it wrote. No model
// name, model list or CLI version is fixed here: a provider that reports no
// such fact leaves it empty.
package providers

import (
	"context"
	"errors"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/adapter/opencode"
	providerspec "github.com/Zen1th53/marshal/internal/adapter/provider"
	"github.com/Zen1th53/marshal/internal/usage"
)

// Model is one model an installed CLI offers.
type Model struct {
	ID   string
	Name string
	// Efforts are the reasoning efforts the CLI advertises for the model;
	// empty when it advertises none.
	Efforts []string
	// Default is set on the model the CLI itself would use.
	Default bool
}

// Catalog is what an installed CLI offers now. Default is the model the CLI
// would use, or "" when it does not say.
type Catalog struct {
	Models  []Model
	Default string
}

// QuotaInput says what a quota read may consult besides the files the
// provider CLI writes itself.
type QuotaInput struct {
	// Root resolves a relative provider state directory.
	Root string
	// MarshalRecords also reads what MARSHAL itself recorded (Claude's last
	// rate_limit_event) or lists on a schedule (agy's model offer, which
	// runs `agy models` at most every two minutes).
	MarshalRecords bool
}

// Provider is every capability a registered provider implements.
type Provider interface {
	// Spec is the provider's identity and on-disk layout.
	Spec() providerspec.Spec
	// Models asks the installed CLI at binary which models it offers now.
	Models(ctx context.Context, binary string, runner adapter.ProcessRunner) (Catalog, error)
	// Quota is the provider's last reported limits.
	Quota(now time.Time, in QuotaInput) usage.Quota
}

// CurrentModelReporter is implemented by a provider whose CLI reports the
// model it would use now, cheaply enough to ask before a probe.
type CurrentModelReporter interface {
	CurrentModel(ctx context.Context, binary string, runner adapter.ProcessRunner) (string, error)
}

// ErrUnknownProvider reports a name no registered provider answers to.
var ErrUnknownProvider = errors.New("unknown provider")

var registry = []Provider{
	codexProvider{spec: providerspec.Must("codex")},
	claudeProvider{spec: providerspec.Must("claude")},
	opencodeProvider{spec: providerspec.Must("opencode")},
	agyProvider{spec: providerspec.Must("agy")},
}

// All is every registered provider, in providerspec.All order.
func All() []Provider { return append([]Provider(nil), registry...) }

// Get is the provider name refers to (its ID or an alias).
func Get(name string) (Provider, bool) {
	spec, ok := providerspec.Lookup(name)
	if !ok {
		return nil, false
	}
	for _, p := range registry {
		if p.Spec().ID == spec.ID {
			return p, true
		}
	}
	return nil, false
}

// CurrentModel is the model the provider's CLI reports it would use now,
// with ok false when the provider's CLI reports none.
func CurrentModel(ctx context.Context, name, binary string, runner adapter.ProcessRunner) (current string, ok bool, err error) {
	p, found := Get(name)
	if !found {
		return "", false, ErrUnknownProvider
	}
	reporter, reports := p.(CurrentModelReporter)
	if !reports {
		return "", false, nil
	}
	current, err = reporter.CurrentModel(ctx, binary, runner)
	return current, true, err
}

// ReadQuotas is every registered provider's quota at now, in All order.
func ReadQuotas(now time.Time, in QuotaInput) []usage.Quota {
	out := make([]usage.Quota, 0, len(registry))
	for _, p := range registry {
		out = append(out, p.Quota(now, in))
	}
	return out
}

// readAgy is usage.ReadAgy; tests replace it to see whether agy is asked.
var readAgy = usage.ReadAgy

type codexProvider struct{ spec providerspec.Spec }

func (p codexProvider) Spec() providerspec.Spec { return p.spec }

func (p codexProvider) Models(ctx context.Context, binary string, runner adapter.ProcessRunner) (Catalog, error) {
	infos, def, err := codex.DiscoverModels(ctx, binary, runner)
	if err != nil {
		return Catalog{}, err
	}
	c := Catalog{Default: def}
	for _, m := range infos {
		c.Models = append(c.Models, Model{ID: m.Slug, Name: m.DisplayName, Efforts: append([]string(nil), m.ReasoningEfforts...), Default: m.IsDefault || (def != "" && m.Slug == def)})
	}
	return c, nil
}

func (p codexProvider) CurrentModel(ctx context.Context, binary string, runner adapter.ProcessRunner) (string, error) {
	_, def, err := codex.DiscoverModels(ctx, binary, runner)
	return def, err
}

func (p codexProvider) Quota(now time.Time, in QuotaInput) usage.Quota {
	dir, err := p.spec.HistoryDir(nil, in.Root)
	if err != nil {
		dir = ""
	}
	return usage.ReadCodex(now, dir)
}

type claudeProvider struct{ spec providerspec.Spec }

func (p claudeProvider) Spec() providerspec.Spec { return p.spec }

// Models is the model Claude Code resolves for a session: its CLI lists no
// catalog, so the one model it reports is the whole answer.
func (p claudeProvider) Models(ctx context.Context, binary string, runner adapter.ProcessRunner) (Catalog, error) {
	infos, def, err := claude.DiscoverModels(ctx, binary, runner)
	if err != nil {
		return Catalog{}, err
	}
	c := Catalog{Default: def}
	for _, m := range infos {
		c.Models = append(c.Models, Model{ID: m.Slug, Name: m.DisplayName, Default: m.IsDefault || (def != "" && m.Slug == def)})
	}
	return c, nil
}

func (p claudeProvider) CurrentModel(ctx context.Context, binary string, runner adapter.ProcessRunner) (string, error) {
	_, def, err := claude.DiscoverModels(ctx, binary, runner)
	return def, err
}

func (p claudeProvider) Quota(now time.Time, in QuotaInput) usage.Quota {
	events := ""
	if in.MarshalRecords {
		events = usage.ClaudeRateLimitCache()
	}
	return usage.ReadClaude(now, usage.ClaudeConfigFile(), events)
}

type opencodeProvider struct{ spec providerspec.Spec }

func (p opencodeProvider) Spec() providerspec.Spec { return p.spec }

func (p opencodeProvider) Models(ctx context.Context, binary string, runner adapter.ProcessRunner) (Catalog, error) {
	ids, err := opencode.DiscoverModels(ctx, binary, runner)
	if err != nil {
		return Catalog{}, err
	}
	var c Catalog
	for _, id := range ids {
		c.Models = append(c.Models, Model{ID: id, Name: id})
	}
	return c, nil
}

func (p opencodeProvider) Quota(time.Time, QuotaInput) usage.Quota {
	return usage.Unexposed(p.spec.ID)
}

type agyProvider struct{ spec providerspec.Spec }

func (p agyProvider) Spec() providerspec.Spec { return p.spec }

func (p agyProvider) Models(ctx context.Context, binary string, runner adapter.ProcessRunner) (Catalog, error) {
	models, err := antigravity.DiscoverModels(ctx, binary, runner)
	if err != nil {
		return Catalog{}, err
	}
	var c Catalog
	for _, m := range models {
		c.Models = append(c.Models, Model{ID: m.ID, Name: m.Name})
	}
	return c, nil
}

func (p agyProvider) Quota(now time.Time, in QuotaInput) usage.Quota {
	seen := ""
	if in.MarshalRecords {
		seen = usage.AgySeenGroupsFile()
	}
	if seen == "" {
		return usage.Unexposed(p.spec.ID)
	}
	return readAgy(now, seen)
}
