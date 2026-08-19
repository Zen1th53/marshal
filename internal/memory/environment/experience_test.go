package environment_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/environment"
)

func TestT147EnvironmentAndExperienceMemory(t *testing.T) {
	ctx := context.Background()
	mgr := environment.NewExperienceManager()
	now := time.Now().UTC()

	linuxEnv := environment.Signature{
		OS:        "linux",
		Arch:      "amd64",
		GoVersion: "go1.24",
		Toolchain: "gcc",
	}

	darwinEnv := environment.Signature{
		OS:        "darwin",
		Arch:      "arm64",
		GoVersion: "go1.24",
		Toolchain: "clang",
	}

	// 1. Record Linux-specific CGO gotcha
	expLinux, err := mgr.RecordExperience(ctx, environment.Experience{
		ID:          "EXP-LINUX-SQLITE-CGO",
		Kind:        environment.ExpGotchaFailureMode,
		Title:       "Linux SQLite CGO Linking Flag",
		Body:        "Requires -ldl -lpthread on Linux GCC toolchain",
		Environment: linuxEnv,
		CreatedAt:   now,
	})
	if err != nil {
		t.Fatalf("RecordExperience: %v", err)
	}

	// 2. Recall in Linux environment -> Must match
	recalledLinux, err := mgr.RecallMatchingExperience(ctx, linuxEnv, "SQLite CGO")
	if err != nil {
		t.Fatalf("RecallMatchingExperience Linux: %v", err)
	}
	if len(recalledLinux) != 1 || recalledLinux[0].ID != expLinux.ID {
		t.Fatalf("expected Linux gotcha recalled in Linux environment, got: %+v", recalledLinux)
	}

	// 3. Recall in Darwin environment -> Must NOT match / leak Linux-specific flag
	recalledDarwin, err := mgr.RecallMatchingExperience(ctx, darwinEnv, "SQLite CGO")
	if err != nil {
		t.Fatalf("RecallMatchingExperience Darwin: %v", err)
	}
	if len(recalledDarwin) != 0 {
		t.Fatalf("expected 0 results for Linux gotcha in Darwin environment, got %d", len(recalledDarwin))
	}
}
