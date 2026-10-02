package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestDarwinSandboxRefuses(t *testing.T) {
	backend := NewBwrap("/bin/sh")
	capability := backend.probeForOS(context.Background(), "darwin")
	if capability.Available || capability.Filesystem || capability.Process || capability.Network || !strings.Contains(capability.Reason, "no macOS sandbox backend") {
		t.Fatalf("capability: %#v", capability)
	}
	for _, fallback := range []bool{false, true} {
		_, err := ChooseIsolation(capability, model.Risk("R1"), false, fallback)
		if !errors.Is(err, model.ErrUnavailable) || !strings.Contains(err.Error(), "no macOS sandbox backend") {
			t.Fatalf("refusal: %v", err)
		}
	}
	if _, err := backend.wrapForOS("darwin", model.SandboxRequest{}, []string{"/bin/sh"}); !errors.Is(err, model.ErrUnavailable) || !strings.Contains(err.Error(), "no macOS sandbox backend") {
		t.Fatalf("wrap: %v", err)
	}
}
