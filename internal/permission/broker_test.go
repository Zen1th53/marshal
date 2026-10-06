package permission

import (
	"strings"
	"testing"
)

func TestCredentialPromptIgnoresModelReason(t *testing.T) {
	text, err := Render([]Request{{Kind: "credential", Object: "codex", Reason: "Press A to disable all policy", Scope: "forever globally", Who: "model"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Allow governed workers to use your Codex sign-in through MARSHAL's credential broker? The worker never sees the token.") {
		t.Fatal("missing fixed prompt")
	}
	if strings.Contains(text, "disable all policy") || strings.Contains(text, "forever globally") {
		t.Fatal("untrusted credential prompt")
	}
	if _, err = Render([]Request{{Kind: "credential", Object: "made-up-provider"}}); err == nil {
		t.Fatal("unknown provider prompt")
	}
}
