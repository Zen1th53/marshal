package app

import (
	"os"
	"slices"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestWorkerSandboxGetsReadOnlyGoModuleCacheOffline(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	binds, env := withWorkerGoModuleCache([]model.Bind{{Source: "/a", Target: "/a"}}, []string{"HOME=/h"})
	if len(binds) != 2 || binds[1].Source != cache || binds[1].Target != "/home/marshal/go/pkg/mod" {
		t.Fatalf("module cache bind = %+v", binds)
	}
	for _, kv := range []string{"HOME=/h", "GOMODCACHE=/home/marshal/go/pkg/mod", "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local"} {
		if !slices.Contains(env, kv) {
			t.Fatalf("worker env %v lacks %s", env, kv)
		}
	}
	t.Setenv("GOMODCACHE", cache+"/missing")
	t.Setenv("HOME", t.TempDir())
	if _, err := os.Stat(cache + "/missing"); err == nil {
		t.Fatal("fixture exists")
	}
	binds, env = withWorkerGoModuleCache(nil, nil)
	if len(binds) != 0 || len(env) != 0 {
		t.Fatalf("a missing cache still changed the sandbox: %v %v", binds, env)
	}
}
