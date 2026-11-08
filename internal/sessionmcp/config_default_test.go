//go:build governed || !unix

package sessionmcp

import (
	"reflect"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestCommunitySandboxRequestKeepsOriginalEnvironment(t *testing.T) {
	request := model.SandboxRequest{Worktree: "/fixture", ExtraEnv: []string{"OPENCODE_CONFIG_CONTENT={", "OPENCODE_CONFIG_CONTENT=duplicate", "UNCHANGED=value"}}
	got, err := SandboxRequest(t.Context(), request)
	if err != nil || !reflect.DeepEqual(got, request) {
		t.Fatalf("Community sandbox request changed: %+v %v", got, err)
	}
}
