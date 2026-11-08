//go:build governed

package app

import (
	"github.com/Zen1th53/marshal/internal/execution"
	"testing"
)

func TestCommunityPreconfiguredExecutionServiceRetainsConstructor(t *testing.T) {
	for _, scenario := range []string{"nil-runtime", "nil-engine", "runtime-engine"} {
		t.Run(scenario, func(t *testing.T) {
			r := openNetpolRuntime(t)
			e, err := execution.NewEngine(execution.EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "nil-runtime" {
				r = nil
			}
			if scenario == "nil-engine" {
				e = nil
			}
			service := NewExecutionServiceWithEngine(r, e)
			if service.Engine() != e {
				t.Fatal("Community constructor replaced engine")
			}
			if e != nil && service.available() != nil {
				t.Fatal(service.available())
			}
		})
	}
}
