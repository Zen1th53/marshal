package provider

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func envOf(m map[string]string) Getenv { return func(k string) string { return m[k] } }

// Every registered provider answers to its ID, its aliases and its harness
// key, and resolves its own state directory without a name check.
func TestEveryProviderIdentityAndState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if len(All()) == 0 {
		t.Fatal("no provider registered")
	}
	seen := map[string]bool{}
	for _, s := range All() {
		t.Run(s.ID, func(t *testing.T) {
			if s.ID == "" || s.Name == "" || s.Binary == "" || s.Harness == "" || s.StateDefault == "" {
				t.Fatalf("incomplete spec %+v", s)
			}
			for _, name := range append([]string{s.ID, strings.ToUpper(s.ID), " " + s.ID + " "}, s.Aliases...) {
				if got, ok := Lookup(name); !ok || got.ID != s.ID {
					t.Fatalf("Lookup(%q) = %+v, %v", name, got, ok)
				}
				if Canonical(name) != s.ID {
					t.Fatalf("Canonical(%q) = %q", name, Canonical(name))
				}
			}
			if got, ok := ByHarness(s.Harness); !ok || got.ID != s.ID {
				t.Fatalf("ByHarness(%q) = %+v, %v", s.Harness, got, ok)
			}
			if HarnessName(s.ID) != s.Harness {
				t.Fatalf("HarnessName(%q) = %q", s.ID, HarnessName(s.ID))
			}
			for _, name := range append([]string{s.ID}, s.Aliases...) {
				if seen[name] {
					t.Fatalf("%q names two providers", name)
				}
				seen[name] = true
			}

			dir, overridden, err := s.StateDir(envOf(nil))
			if err != nil || overridden || dir != filepath.Join(home, s.StateDefault) {
				t.Fatalf("default StateDir = %q, %v, %v", dir, overridden, err)
			}
			if s.StateEnv != "" {
				set := filepath.Join(home, "elsewhere")
				dir, overridden, err = s.StateDir(envOf(map[string]string{s.StateEnv: "  " + set + " "}))
				if err != nil || !overridden || dir != filepath.Join(set, s.StateEnvSubdir) {
					t.Fatalf("overridden StateDir = %q, %v, %v", dir, overridden, err)
				}
				rel, err := s.StateDirIn(envOf(map[string]string{s.StateEnv: "rel"}), "/project")
				if err != nil || rel != filepath.Join("/project", "rel", s.StateEnvSubdir) {
					t.Fatalf("relative StateDirIn = %q, %v", rel, err)
				}
				if got := StateEnvs(); !contains(got, s.StateEnv) {
					t.Fatalf("StateEnvs %v misses %s", got, s.StateEnv)
				}
			}
			hist, err := s.HistoryDir(envOf(nil), "/project")
			if s.HistorySubdir == "" {
				if !errors.Is(err, ErrNoHistoryDir) {
					t.Fatalf("HistoryDir = %q, %v; want ErrNoHistoryDir", hist, err)
				}
			} else if err != nil || hist != filepath.Join(home, s.StateDefault, s.HistorySubdir) {
				t.Fatalf("HistoryDir = %q, %v", hist, err)
			}
		})
	}
	if _, ok := Lookup("no-such-provider"); ok {
		t.Fatal("unknown provider resolved")
	}
	if Canonical(" Other ") != "other" || HarnessName("Other") != "Other" {
		t.Fatal("unknown names must pass through")
	}
	if len(IDs()) != len(All()) {
		t.Fatal("IDs and All disagree")
	}
}

func TestEnvSliceLastAssignmentWinsAndFallsBack(t *testing.T) {
	t.Setenv("MARSHAL_PROVIDER_TEST", "process")
	get := EnvSlice([]string{"MARSHAL_PROVIDER_TEST=first", "MARSHAL_PROVIDER_TEST=last", "OTHER=  "})
	if get("MARSHAL_PROVIDER_TEST") != "last" {
		t.Fatalf("got %q", get("MARSHAL_PROVIDER_TEST"))
	}
	if EnvSlice(nil)("MARSHAL_PROVIDER_TEST") != "process" {
		t.Fatal("no fallback to the process environment")
	}
}

func TestAllReturnsCopies(t *testing.T) {
	a := All()
	a[0].ID = "changed"
	for i := range a {
		a[i].Aliases = append(a[i].Aliases, "changed")
	}
	if All()[0].ID == "changed" {
		t.Fatal("All exposes the registry")
	}
	if _, ok := Lookup("changed"); ok {
		t.Fatal("All exposes aliases")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
