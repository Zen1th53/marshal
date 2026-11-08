//go:build governed

package tui

import (
	"context"
	"github.com/Zen1th53/marshal/internal/permission"
)

type buildPermissionState struct{}

const buildMarker = ""
const buildRoleAuthority = ""

func (w *Workspace) routeBuildPermission(context.Context, permission.Request) bool { return false }
func (w *Workspace) consumeBuildDecision([]byte) bool                              { return false }
func (w *Workspace) decorateMarshalStatus(_ context.Context, base string) string   { return base }

func (*Workspace) registerPrivateProcess05Questions(context.Context, []permission.Request) {}
