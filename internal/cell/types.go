package cell

import (
	"context"
	"path/filepath"
	"strings"
	"time"
)

type CellID string
type TaskID string
type BackendKind string
type SecretRef string

const (
	BackendNative     BackendKind = "native"
	BackendBubblewrap BackendKind = "bubblewrap"
)

type Spec struct {
	TaskID         TaskID      `json:"task_id"`
	Workspace      string      `json:"workspace"`
	Backend        BackendKind `json:"backend"`
	Capabilities   []string    `json:"capabilities,omitempty"`
	NetworkProfile string      `json:"network_profile"`
	CPUQuota       int         `json:"cpu_quota"`
	MemoryBytes    int64       `json:"memory_bytes"`
	SecretRefs     []SecretRef `json:"secret_refs,omitempty"`
}

func NewSpec(spec Spec) (Spec, error) {
	if err := spec.Validate(); err != nil {
		return Spec{}, err
	}
	spec.Capabilities = append([]string(nil), spec.Capabilities...)
	spec.SecretRefs = append([]SecretRef(nil), spec.SecretRefs...)
	return spec, nil
}

func (s Spec) Validate() error {
	if !validID(string(s.TaskID)) || !validWorkspace(s.Workspace) {
		if !validWorkspace(s.Workspace) {
			return ErrScopeEscape
		}
		return ErrPrepareFailed
	}
	if s.Backend != BackendNative && s.Backend != BackendBubblewrap {
		return ErrBackendUnavailable
	}
	if s.CPUQuota < 0 || s.MemoryBytes < 0 {
		return ErrPrepareFailed
	}
	for _, capability := range s.Capabilities {
		if !validText(capability) {
			return ErrPrepareFailed
		}
	}
	for _, ref := range s.SecretRefs {
		if !validText(string(ref)) {
			return ErrPrepareFailed
		}
	}
	return nil
}

type Handle struct {
	ID        CellID      `json:"id"`
	TaskID    TaskID      `json:"task_id"`
	Backend   BackendKind `json:"backend"`
	Workspace string      `json:"workspace"`
	CreatedAt time.Time   `json:"created_at"`
}

func (h Handle) Validate() error {
	if !validID(string(h.ID)) || !validID(string(h.TaskID)) {
		return ErrPrepareFailed
	}
	if !validWorkspace(h.Workspace) {
		return ErrScopeEscape
	}
	if h.Backend != BackendNative && h.Backend != BackendBubblewrap {
		return ErrBackendUnavailable
	}
	if !h.CreatedAt.IsZero() && h.CreatedAt.Location() != time.UTC {
		return ErrPrepareFailed
	}
	return nil
}

type ExecRequest struct {
	Command []string          `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
}

type ExecResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   []byte `json:"-"`
	Stderr   []byte `json:"-"`
}

type Backend interface {
	Prepare(context.Context, Spec) (Handle, error)
	Exec(context.Context, Handle, ExecRequest) (ExecResult, error)
	Destroy(context.Context, Handle) error
}

func validID(value string) bool {
	return validText(value) && !strings.Contains(value, "/") && !strings.Contains(value, "\\")
}

func validWorkspace(value string) bool {
	if !validText(value) || !filepath.IsAbs(value) {
		return false
	}
	clean := filepath.Clean(value)
	return clean == value && clean != string(filepath.Separator) && !strings.Contains(value, string(filepath.Separator)+".."+string(filepath.Separator))
}

func validText(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
