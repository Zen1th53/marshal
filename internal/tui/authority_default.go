//go:build governed

package tui

import (
	"context"
	"github.com/Zen1th53/marshal/internal/permission"
)

type buildPermissionState struct{}

const buildMarker = ""

// launchEarlierWorkThroughQueue: the launch scan loads earlier sessions by
// queueing the ordinary read request (shipped decides it automatically);
// governed loads directly only under an operator grant already on record.
const launchEarlierWorkThroughQueue = false

const continuationMemoryHint = "Summarise completed work, unfinished work, decisions and conventions from the supplied project-scoped data. Use the candidate IDs and provenance supplied above; propose each candidate by atomically writing {\"action\":\"memory\",\"id\":\"candidate-id\"} to a fresh .marshal/proposals/<unique-request-id>.json file; do not print the payload in chat. MARSHAL will show a popup; press A to apply.\n"
const buildRoleAuthority = ""

func (w *Workspace) routeBuildPermission(context.Context, permission.Request) bool { return false }
func (w *Workspace) consumeBuildDecision([]byte) bool                              { return false }
func (w *Workspace) decorateMarshalStatus(_ context.Context, base string) string   { return base }

func (*Workspace) registerPrivateProcess05Questions(context.Context, []permission.Request) {}
