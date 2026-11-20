package providers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	providerspec "github.com/Zen1th53/marshal/internal/adapter/provider"
	"github.com/Zen1th53/marshal/internal/usage"
)

// fakeCLI answers each provider's model command with what that CLI prints.
// The model names are fixtures for parsing, not a list Marshal ships.
type fakeCLI struct{ calls []string }

func (f *fakeCLI) Run(_ context.Context, c adapter.Command) (adapter.ProcessResult, error) {
	f.calls = append(f.calls, c.Path+" "+strings.Join(c.Args, " "))
	var out string
	switch c.Path {
	case "/bin/codex":
		out = `{"models":[{"slug":"fixture-a","display_name":"A","is_default":true,"supported_reasoning_levels":[{"effort":"low"},{"effort":"high"}]},{"slug":"fixture-b"}]}`
	case "/bin/claude":
		if len(c.Args) == 1 && c.Args[0] == "--help" {
			out = "Options:\n  --effort <level>   Effort level (low, high)\n  --model <model>    Model. Use an alias (e.g.\n                     'fixture-alias') or a full name.\n"
		} else {
			out = `{"type":"system","subtype":"init","model":"fixture-claude"}` + "\n"
		}
	case "/bin/opencode":
		out = "Some banner line\nvendor/fixture-x\nvendor/fixture-y\nvendor/fixture-x\n"
	case "/bin/agy":
		out = "Fetching available models\nfixture-g1\tFixture G1\nfixture-g2\tFixture G2\n"
	}
	return adapter.ProcessResult{Stdout: []byte(out)}, nil
}

func isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", home+"/cache")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CODEX_CONFIG", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("MARSHAL_NO_USAGE_PROBES", "1")
}

func TestRegistryCoversEverySpec(t *testing.T) {
	specs := providerspec.All()
	all := All()
	if len(all) != len(specs) {
		t.Fatalf("%d providers registered for %d specs", len(all), len(specs))
	}
	for i, p := range all {
		if p.Spec().ID != specs[i].ID {
			t.Fatalf("registry[%d] = %s, want %s", i, p.Spec().ID, specs[i].ID)
		}
		for _, name := range append([]string{p.Spec().ID}, p.Spec().Aliases...) {
			if got, ok := Get(name); !ok || got.Spec().ID != p.Spec().ID {
				t.Fatalf("Get(%q) = %v, %v", name, got, ok)
			}
		}
	}
	if _, ok := Get("no-such-provider"); ok {
		t.Fatal("unknown provider resolved")
	}
	if _, _, err := CurrentModel(context.Background(), "no-such-provider", "", nil); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("CurrentModel of unknown = %v", err)
	}
}

// Every provider's catalog comes from its installed CLI's own output.
func TestEveryProviderModelsComeFromTheCLI(t *testing.T) {
	isolate(t)
	for _, p := range All() {
		t.Run(p.Spec().ID, func(t *testing.T) {
			cli := &fakeCLI{}
			binary := "/bin/" + p.Spec().Binary
			catalog, err := p.Models(context.Background(), binary, cli)
			if err != nil {
				t.Fatalf("Models: %v", err)
			}
			if len(cli.calls) == 0 || !strings.HasPrefix(cli.calls[0], binary+" ") {
				t.Fatalf("the installed CLI was not asked: %v", cli.calls)
			}
			fromCLI := 0
			for _, m := range catalog.Models {
				if m.ID == "" {
					t.Fatalf("model without an ID: %+v", catalog)
				}
				if strings.Contains(m.ID, "fixture") {
					fromCLI++
				}
			}
			if fromCLI == 0 {
				t.Fatalf("no model from the CLI output: %+v", catalog)
			}
			if catalog.Default != "" {
				found := false
				for _, m := range catalog.Models {
					found = found || (m.ID == catalog.Default && m.Default)
				}
				if !found {
					t.Fatalf("default %q is not a listed default model: %+v", catalog.Default, catalog)
				}
			}
			current, reports, err := CurrentModel(context.Background(), p.Spec().ID, binary, &fakeCLI{})
			if err != nil {
				t.Fatalf("CurrentModel: %v", err)
			}
			if reports != (catalog.Default != "") || current != catalog.Default {
				t.Fatalf("CurrentModel = %q, %v; catalog default %q", current, reports, catalog.Default)
			}
		})
	}
}

func TestModelsParsePerCLI(t *testing.T) {
	isolate(t)
	want := map[string]string{
		"codex":    "fixture-a[low high]* fixture-b[]",
		"claude":   "fixture-alias[low high] fixture-claude[low high]*",
		"opencode": "vendor/fixture-x[] vendor/fixture-y[]",
		"agy":      "fixture-g1[] fixture-g2[]",
	}
	for _, p := range All() {
		catalog, err := p.Models(context.Background(), "/bin/"+p.Spec().Binary, &fakeCLI{})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, m := range catalog.Models {
			if !strings.Contains(m.ID, "fixture") {
				continue
			}
			s := m.ID + "[" + strings.Join(m.Efforts, " ") + "]"
			if m.Default {
				s += "*"
			}
			got = append(got, s)
		}
		w, ok := want[p.Spec().ID]
		if !ok {
			t.Fatalf("no parse expectation for %s", p.Spec().ID)
		}
		if strings.Join(got, " ") != w {
			t.Fatalf("%s: got %q, want %q", p.Spec().ID, strings.Join(got, " "), w)
		}
	}
}

func TestEveryProviderQuotaIsItsOwnAndNeverInvented(t *testing.T) {
	isolate(t)
	now := time.Now()
	for _, records := range []bool{false, true} {
		qs := ReadQuotas(now, QuotaInput{Root: t.TempDir(), MarshalRecords: records})
		if len(qs) != len(All()) {
			t.Fatalf("ReadQuotas = %d quotas", len(qs))
		}
		for i, p := range All() {
			q := qs[i]
			if q.Provider != p.Spec().ID {
				t.Fatalf("quota %d is %q, want %q", i, q.Provider, p.Spec().ID)
			}
			// Nothing was written on this empty machine, so nothing is known.
			if q.Known || len(q.Windows) != 0 || q.Reason == "" {
				t.Fatalf("%s quota invented: %+v", q.Provider, q)
			}
		}
	}
}

// agy is only listed when the caller asks for MARSHAL's scheduled records.
func TestQuotaRunsAgyOnlyWhenAsked(t *testing.T) {
	isolate(t)
	saved := readAgy
	t.Cleanup(func() { readAgy = saved })
	called := false
	readAgy = func(now time.Time, seen string) usage.Quota { called = true; return usage.Unexposed("agy") }
	p, _ := Get("agy")
	if q := p.Quota(time.Now(), QuotaInput{}); q.Reason != usage.NotExposed || called {
		t.Fatalf("agy read without being asked: %+v %v", q, called)
	}
	p.Quota(time.Now(), QuotaInput{MarshalRecords: true})
	if !called {
		t.Fatal("agy not read when asked")
	}
}

// Claude's session probe can fail (signed out, offline); the aliases its
// installed help names are still its catalog, with no default invented.
func TestClaudeModelsFallBackToItsHelp(t *testing.T) {
	isolate(t)
	runner := runnerFunc(func(c adapter.Command) adapter.ProcessResult {
		if len(c.Args) == 1 && c.Args[0] == "--help" {
			return adapter.ProcessResult{Stdout: []byte("  --model <model>  Use an alias (e.g. 'fixture-a' or 'fixture-b')\n")}
		}
		return adapter.ProcessResult{ExitCode: 1}
	})
	p, _ := Get("claude")
	catalog, err := p.Models(context.Background(), "/bin/claude", runner)
	if err != nil || catalog.Default != "" || len(catalog.Models) != 2 || catalog.Models[0].ID != "fixture-a" {
		t.Fatalf("catalog = %+v, %v", catalog, err)
	}
}

type runnerFunc func(adapter.Command) adapter.ProcessResult

func (f runnerFunc) Run(_ context.Context, c adapter.Command) (adapter.ProcessResult, error) {
	return f(c), nil
}
