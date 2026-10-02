package sandbox

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/Zen1th53/marshal/internal/model"
)

// Backend owns capability verification and command envelopes.
type Backend interface {
	Probe(context.Context) model.IsolationCapability
	Wrap(model.SandboxRequest, []string) (model.CommandSpec, error)
}

func NewBackend() (Backend, error) { return NewBackendForOS(runtime.GOOS) }

func NewBackendForOS(goos string) (Backend, error) {
	switch goos {
	case "darwin":
		return NewSeatbelt("/usr/bin/sandbox-exec"), nil
	case "linux":
		path, err := TrustedBwrapPath()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", model.ErrUnavailable, err)
		}
		return NewBwrap(path), nil
	default:
		return nil, fmt.Errorf("%w: %s", model.ErrUnavailable, PlatformUnavailableReason(goos))
	}
}

func ProbeForOS(ctx context.Context, goos string) model.IsolationCapability {
	backend, err := NewBackendForOS(goos)
	if err != nil {
		return model.IsolationCapability{Level: model.IsolationBlocked, Reason: err.Error()}
	}
	return backend.Probe(ctx)
}

func TrustedBwrapPath() (string, error) {
	for _, path := range []string{"/usr/bin/bwrap", "/bin/bwrap"} {
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o022 == 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("trusted bubblewrap binary is unavailable")
}
