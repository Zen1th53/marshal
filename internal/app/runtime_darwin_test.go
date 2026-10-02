//go:build darwin

package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestDarwinGovernedAdapterRefusesBeforeProviderLookup(t *testing.T) {
	runtime := &Runtime{}
	for _, name := range []string{"codex", "claude", "gemini", "opencode"} {
		candidate, grant, err := runtime.resolveAdapter(context.Background(), name, model.Task{}, "", "", false, "", "")
		if candidate != nil || grant != "" || !errors.Is(err, model.ErrUnavailable) || !strings.Contains(err.Error(), "no macOS sandbox backend") {
			t.Fatalf("%s: candidate=%v grant=%q error=%v", name, candidate, grant, err)
		}
	}
}
