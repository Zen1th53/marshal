package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestProviderDialectQualification(t *testing.T) {
	for _, tc := range []struct{ provider, version, supported, unsupported string }{
		{"codex", "codex-cli 0.159.2", "exec fork", "mcp enable"},
		{"claude", "2.1.286 (Claude Code)", "plugin install", "plugin get"},
		{"opencode", "1.18.16", "mcp list", "mcp remove"},
		{"agy", "1.2.7", "plugin install", "fork"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			d := QualifiedProviderDialect(tc.provider, tc.version)
			if d.Operation(tc.supported).Status != ProviderSupported {
				t.Fatal("supported operation not qualified")
			}
			if d.Operation(tc.unsupported).Status != ProviderUnsupported {
				t.Fatal("unsupported operation not refused")
			}
			if d.Operation("future-operation").Status != ProviderUnknown {
				t.Fatal("unknown became qualified")
			}
			if QualifiedProviderDialect(tc.provider, "999.0.0").Operation(tc.supported).Status != ProviderUnknown {
				t.Fatal("unobserved version became qualified")
			}
			if err := d.Check(tc.unsupported, true); err == nil {
				t.Fatal("unsupported operation admitted")
			}
			if err := d.Check("future-operation", true); err != nil {
				t.Fatal("unknown pass-through removed", err)
			}
		})
	}
}

func TestProviderDialectObservationAndReplacement(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(t.TempDir())
	t.Setenv("PATH", dir)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	for _, tc := range []struct{ provider, version string }{{"codex", "codex-cli 0.159.2"}, {"claude", "2.1.286 (Claude Code)"}, {"opencode", "1.18.16"}, {"agy", "1.2.7"}} {
		path := filepath.Join(dir, tc.provider)
		log := filepath.Join(t.TempDir(), "probes")
		t.Setenv("PROBE_LOG", log)
		write := func(version string) {
			script := "#!/bin/sh\n[ \"$#\" = 1 ] && [ \"$1\" = --version ] || exit 9\nprintf '%s\\n' \"$1\" >> \"$PROBE_LOG\"\nprintf '%s\\n' '" + version + "'\n"
			// Replace by rename: rewriting the file in place can race a fork in
			// another test that inherited the write descriptor, and exec then
			// fails with ETXTBSY.
			if err := os.WriteFile(path+".new", []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path+".new", path); err != nil {
				t.Fatal(err)
			}
		}
		write(tc.version)
		for i := 0; i < 2; i++ {
			d := ObserveProviderDialect(context.Background(), tc.provider)
			if d.QualifiedVersion == "" || d.Binary != path {
				t.Fatalf("observation: %+v", d)
			}
		}
		data, _ := os.ReadFile(log)
		if string(data) != "--version\n" {
			t.Fatalf("cache repeated process: %q", data)
		}
		write("999.0.0")
		d := ObserveProviderDialect(context.Background(), tc.provider)
		if d.QualifiedVersion != "" || d.Operation("resume").Status != ProviderUnknown {
			t.Fatal("replacement retained stale qualification")
		}
		write(tc.version)
		if ObserveProviderDialect(context.Background(), tc.provider).QualifiedVersion == "" {
			t.Fatal("qualification did not recover")
		}
	}
}

func TestProviderDialectProbeBoundedAndCancelled(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(t.TempDir())
	t.Setenv("PATH", dir)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nexec /bin/sleep 10\n"), 0700); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	d := ObserveProviderDialect(context.Background(), "codex")
	if d.Version != "UNKNOWN" || d.QualifiedVersion != "" || time.Since(start) > 8*time.Second {
		t.Fatalf("unbounded/qualified failed probe: %+v %v", d, time.Since(start))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start = time.Now()
	ObserveProviderDialect(ctx, "codex")
	if time.Since(start) > time.Second {
		t.Fatal("cancelled probe launched")
	}
	output := &providerProbeOutput{}
	payload := []byte(strings.Repeat("x", 100*1024))
	n, err := output.Write(payload)
	if err != nil || n != len(payload) || len(output.data) != 64*1024 || !output.truncated {
		t.Fatal("probe output is not bounded")
	}
}

func TestProviderDialectLatestForkMode(t *testing.T) {
	d := QualifiedProviderDialect("codex", "codex-cli 0.159.2")
	if d.WrapperOperation("fork", false).Status != ProviderSupported {
		t.Fatal("named headless fork lost support")
	}
	if c := d.WrapperOperation("fork --last", true); c.Status != ProviderSupported || !c.TerminalOnly {
		t.Fatalf("terminal latest fork: %+v", c)
	}
	if c := d.WrapperOperation("fork --last", false); c.Status != ProviderUnsupported || !c.TerminalOnly {
		t.Fatalf("headless latest fork: %+v", c)
	}
	if err := d.Check(ProviderArgOperation("codex", []string{"exec", "fork", "--last"}), false); err == nil || !strings.Contains(err.Error(), "terminal-only") {
		t.Fatal("unsupported headless flag admitted", err)
	}
	if QualifiedProviderDialect("codex", "codex-cli 999.0.0").Operation("exec fork --last").Status != ProviderUnknown {
		t.Fatal("unknown release was assigned a negative capability")
	}
}

func TestProviderDialectPositionalValuesAreNotCommands(t *testing.T) {
	for _, tc := range []struct{ provider, version, value string }{
		{"claude", "2.1.286 (Claude Code)", "review"},
		{"opencode", "1.18.16", "review"},
		{"agy", "1.2.7", "fork"},
	} {
		d := QualifiedProviderDialect(tc.provider, tc.version)
		for _, args := range [][]string{{tc.value}, {"--model", "ModelCase", tc.value}} {
			op := ProviderArgOperation(tc.provider, args)
			if err := d.Check(op, true); err != nil {
				t.Fatalf("opaque native argument value was refused as a command: %s %q: %v", tc.provider, args, err)
			}
			if d.Operation(op).Status != ProviderUnknown {
				t.Fatal("opaque CLI argument became qualified")
			}
			if got := NormalizeProviderArgs(tc.provider, args); !reflect.DeepEqual(got, args) {
				t.Fatalf("opaque argument changed: got %q want %q", got, args)
			}
		}
	}
}

func TestProviderDialectAliasNormalization(t *testing.T) {
	for _, tc := range []struct {
		provider    string
		input, want []string
	}{
		{"codex", []string{"plugins", "INSTALL", "name"}, []string{"plugin", "add", "name"}},
		{"codex", []string{"mcp", "delete", "name"}, []string{"mcp", "remove", "name"}},
		{"claude", []string{"plugins", "rm", "name"}, []string{"plugin", "uninstall", "name"}},
		{"opencode", []string{"auth", "list"}, []string{"providers", "list"}},
		{"agy", []string{"plugins", "remove", "Name"}, []string{"plugin", "uninstall", "Name"}},
		{"codex", []string{"--model", "ModelCase", "plugins", "install", "Name"}, []string{"--model", "ModelCase", "plugin", "add", "Name"}},
		{"codex", []string{"Fix", "This", "Bug"}, []string{"Fix", "This", "Bug"}},
		{"codex", []string{"exec", "RESUME"}, []string{"exec", "RESUME"}},
		{"codex", []string{"REVIEW"}, []string{"REVIEW"}},
		{"opencode", []string{"plugin", "GitHub/Module"}, []string{"plugin", "GitHub/Module"}},
		{"claude", []string{"plugin", "FutureVerb", "Name"}, []string{"plugin", "FutureVerb", "Name"}},
		{"claude", []string{"--", "MCP", "REMOVE"}, []string{"--", "MCP", "REMOVE"}},
	} {
		original := append([]string(nil), tc.input...)
		got := NormalizeProviderArgs(tc.provider, tc.input)
		if !reflect.DeepEqual(got, tc.want) || !reflect.DeepEqual(tc.input, original) {
			t.Fatalf("%s got=%v want=%v input=%v", tc.provider, got, tc.want, tc.input)
		}
	}
}
