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

func fixtureDialect(t *testing.T, provider string) ProviderDialect {
	t.Helper()
	return ProbeProviderDialect(provider, NormalizeProviderVersion(helpFixtureVersions[provider]), fixtureHelp(t, provider, nil))
}

// writeHelpDouble installs a provider double that prints version for
// --version and the fixture help for `<path> --help`, logging each probe.
func writeHelpDouble(t *testing.T, path, fixtures, version, log string) {
	t.Helper()
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n" +
		"if [ \"$#\" = 1 ] && [ \"$1\" = --version ]; then printf '%s\\n' '" + version + "'; exit 0; fi\n" +
		"last=; for a do last=$a; done; [ \"$last\" = --help ] || exit 9\n" +
		"name=top; [ \"$#\" -gt 1 ] && name=$(printf '%s-' \"$@\" | /usr/bin/sed 's/--help-$//; s/-$//')\n" +
		"/bin/cat '" + fixtures + "'/\"$name\".txt 2>/dev/null; exit 0\n"
	// Replace by rename: rewriting the file in place can race a fork in
	// another test that inherited the write descriptor, and exec then fails
	// with ETXTBSY.
	if err := os.WriteFile(path+".new", []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".new", path); err != nil {
		t.Fatal(err)
	}
}

// The grammar is read once per binary and version; a new version is read
// afresh, and an unchanged binary does not start a process again.
func TestProviderDialectObservationAndReplacement(t *testing.T) {
	fixtures, err := filepath.Abs(filepath.Join("testdata", "providerhelp"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Chdir(t.TempDir())
	t.Setenv("PATH", dir)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	for provider, version := range helpFixtureVersions {
		path := filepath.Join(dir, provider)
		log := filepath.Join(t.TempDir(), "probes")
		count := func(arg string) int {
			data, _ := os.ReadFile(log)
			n := 0
			for _, line := range strings.Split(string(data), "\n") {
				if line == arg {
					n++
				}
			}
			return n
		}
		writeHelpDouble(t, path, filepath.Join(fixtures, provider), version, log)
		for i := 0; i < 2; i++ {
			d := ObserveProviderDialect(context.Background(), provider)
			if !d.Probed || d.Binary != path || d.Version != NormalizeProviderVersion(version) || len(d.Missing()) != 0 {
				t.Fatalf("%s observation: %+v", provider, d)
			}
		}
		if count("--version") != 1 || count("--help") != 1 {
			data, _ := os.ReadFile(log)
			t.Fatalf("%s cache repeated a process: %q", provider, data)
		}
		writeHelpDouble(t, path, filepath.Join(fixtures, provider), "999.0.0", log)
		d := ObserveProviderDialect(context.Background(), provider)
		if !d.Probed || d.Version != "999.0.0" || count("--help") != 2 {
			t.Fatalf("%s new version was not read afresh: %+v", provider, d)
		}
		if d.Operation("resume").Status != ProviderSupported {
			t.Fatalf("%s at an unseen version lost its grammar: %+v", provider, d.Operation("resume"))
		}
		writeHelpDouble(t, path, filepath.Join(fixtures, provider), version, log)
		if d := ObserveProviderDialect(context.Background(), provider); !d.Probed || count("--help") != 2 {
			t.Fatalf("%s grammar of a version already read was read again", provider)
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
	// The bound allows a cold node-based CLI (Claude Code takes over 3 s) to
	// answer, and still stops a CLI that hangs.
	if d.Version != "UNKNOWN" || d.Probed || time.Since(start) > 7*time.Second {
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
	d := fixtureDialect(t, "codex")
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
	if UnprobedProviderDialect("codex", "999.0.0").Operation("exec fork --last").Status != ProviderUnknown {
		t.Fatal("a CLI whose help was not read was assigned a negative capability")
	}
}

func TestProviderDialectPositionalValuesAreNotCommands(t *testing.T) {
	for _, tc := range []struct{ provider, value string }{
		{"claude", "review"},
		{"opencode", "review"},
		{"agy", "fork"},
	} {
		d := fixtureDialect(t, tc.provider)
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

func TestProviderDialectProbeDoesNotHoldCacheLock(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	t.Setenv("PATH", dir)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	t.Setenv("MARSHAL_VERSION_MARKER", marker)
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\n: > \"$MARSHAL_VERSION_MARKER\"\nexec /bin/sleep 6\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); ObserveProviderDialect(ctx, "codex") }()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("version probe did not start")
		}
		time.Sleep(time.Millisecond)
	}
	locked := providerDialectCache.TryLock()
	if locked {
		providerDialectCache.Unlock()
	} else {
		t.Error("provider cache mutex held across external CLI execution")
	}
	cancel()
	<-done
}
