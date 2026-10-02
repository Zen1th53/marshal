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
	if capability.Available || capability.Filesystem || capability.Process || capability.Network || !strings.Contains(capability.Reason, "bubblewrap requires Linux process namespaces") {
		t.Fatalf("capability: %#v", capability)
	}
	for _, fallback := range []bool{false, true} {
		_, err := ChooseIsolation(capability, model.Risk("R1"), false, fallback)
		if !errors.Is(err, model.ErrUnavailable) || !strings.Contains(err.Error(), "bubblewrap requires Linux process namespaces") {
			t.Fatalf("refusal: %v", err)
		}
	}
	if _, err := backend.wrapForOS("darwin", model.SandboxRequest{}, []string{"/bin/sh"}); !errors.Is(err, model.ErrUnavailable) || !strings.Contains(err.Error(), "bubblewrap requires Linux process namespaces") {
		t.Fatalf("wrap: %v", err)
	}
}

func TestBackendSelectionForOS(t *testing.T) {
	backend, err := NewBackendForOS("darwin")
	if err != nil {
		t.Fatal(err)
	}
	seatbelt, ok := backend.(*Seatbelt)
	if !ok || seatbelt.binary != "/usr/bin/sandbox-exec" {
		t.Fatalf("backend: %#v", backend)
	}
	if _, err := NewBackendForOS("unsupported"); !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("unsupported: %v", err)
	}
}

func TestChooseSeatbeltIsolation(t *testing.T) {
	capability := model.IsolationCapability{Level: model.IsolationSeatbelt, Available: true, Filesystem: true, Reason: "no process namespace"}
	chosen, err := ChooseIsolation(capability, model.Risk("R1"), true, false)
	if err != nil || chosen.Level != model.IsolationSeatbelt || chosen.Process || !chosen.Network {
		t.Fatalf("chosen: %#v %v", chosen, err)
	}
	capability.Available = false
	capability.Reason = "seatbelt live probe rejected"
	for _, fallback := range []bool{false, true} {
		blocked, err := ChooseIsolation(capability, model.Risk("R1"), false, fallback)
		if !errors.Is(err, model.ErrUnavailable) || blocked.Level != model.IsolationBlocked || blocked.Reason != capability.Reason {
			t.Fatalf("blocked: %#v %v", blocked, err)
		}
	}
}
