package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/providers"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/worker"
)

const (
	nativeModelListTimeout = 20 * time.Second
	nativeModelListLines   = 200
	// providerModelsTTL is how long a provider's answer is reused.
	providerModelsTTL = 10 * time.Minute
)

var providerModelsCache = struct {
	sync.Mutex
	rows map[string]providerModelsRow
}{rows: map[string]providerModelsRow{}}

type providerModelsRow struct {
	at      time.Time
	catalog providers.Catalog
}

// cachedProviderModels is p.Models for the installed CLI at binary, reused
// for providerModelsTTL per provider and binary.
func cachedProviderModels(ctx context.Context, p providers.Provider, binary string, runner adapter.ProcessRunner) (providers.Catalog, error) {
	key := p.Spec().ID + "\x00" + binary
	providerModelsCache.Lock()
	row, ok := providerModelsCache.rows[key]
	providerModelsCache.Unlock()
	if ok && time.Since(row.at) < providerModelsTTL {
		return row.catalog, nil
	}
	catalog, err := p.Models(ctx, binary, runner)
	if err != nil {
		return providers.Catalog{}, err
	}
	providerModelsCache.Lock()
	if len(providerModelsCache.rows) >= 64 {
		providerModelsCache.rows = map[string]providerModelsRow{}
	}
	providerModelsCache.rows[key] = providerModelsRow{at: time.Now(), catalog: catalog}
	providerModelsCache.Unlock()
	return catalog, nil
}

// listNativeModels lists the models the provider's installed CLI offers, as
// its registered Provider.Models reads them, without opening a terminal.
func listNativeModels(ctx context.Context, provider, title string) string {
	d := app.ObserveProviderDialect(ctx, provider)
	if d.Binary == "" {
		return fmt.Sprintf("%s MODELS: %s is not installed, so there are no models to list. Install it, then run /models %s again.", title, provider, provider)
	}
	p, ok := providers.Get(provider)
	if !ok {
		return fmt.Sprintf("%s MODELS: %s is not a registered provider.", title, provider)
	}
	ctx, cancel := context.WithTimeout(ctx, nativeModelListTimeout)
	defer cancel()
	catalog, err := cachedProviderModels(ctx, p, d.Binary, worker.New(nativeModelListTimeout, 2*time.Second, 8<<20))
	if ctx.Err() != nil {
		return fmt.Sprintf("%s MODELS: %s did not answer within %s. Run /%s in its own session to browse models.", title, provider, nativeModelListTimeout, provider)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		reason := err.Error()
		if len(reason) > 300 {
			reason = reason[:300] + "…"
		}
		if strings.Contains(reason, "listed no models") {
			return fmt.Sprintf("%s MODELS: %s reported no models. Sign in with /login %s and try again.", title, provider, provider)
		}
		return fmt.Sprintf("%s MODELS: could not list models: %s", title, reason)
	}
	if len(catalog.Models) == 0 {
		return fmt.Sprintf("%s MODELS: %s reported no models. Sign in with /login %s and try again.", title, provider, provider)
	}
	return renderProviderCatalog(title, provider, catalog, "")
}

// renderProviderCatalog is one view of a provider's catalog for every
// provider: each model the CLI reported, its name when the CLI gives one,
// the efforts it advertises for it, and which is the CLI's default and the
// owner's selection.
func renderProviderCatalog(title, provider string, catalog providers.Catalog, selected string) string {
	var b strings.Builder
	header := fmt.Sprintf("%s MODELS (%d", title, len(catalog.Models))
	if catalog.Default != "" {
		header += ", default: " + catalog.Default
	}
	b.WriteString(header + "):\n")
	shown := catalog.Models
	if len(shown) > nativeModelListLines {
		shown = shown[:nativeModelListLines]
	}
	for _, m := range shown {
		marker := "  "
		if selected != "" && m.ID == selected {
			marker = "▶ "
		}
		line := fmt.Sprintf(" %s%-28s", marker, m.ID)
		if m.Name != "" && m.Name != m.ID {
			line += fmt.Sprintf(" %-28s", m.Name)
		}
		if len(m.Efforts) > 0 {
			line += " efforts: " + strings.Join(m.Efforts, ", ")
		}
		if m.Default {
			line += " (default)"
		}
		if selected != "" && m.ID == selected {
			line += " [SELECTED]"
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	if more := len(catalog.Models) - len(shown); more > 0 {
		fmt.Fprintf(&b, "  … and %d more (run %s models in a terminal for the full list)\n", more, provider)
	}
	return strings.TrimRight(b.String(), "\n")
}
