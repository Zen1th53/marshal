package tui

import (
	"github.com/Zen1th53/marshal/internal/workerenv"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeAuthProbeOperationalFailure(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "codex")
	for _, output := range []string{"Error reading credentials: Operation not permitted", "database is locked", ""} {
		if err := os.WriteFile(binary, []byte("#!/bin/sh\necho '"+output+"' >&2\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := defaultAuthPreflight(t.Context(), "codex", binary, nil); err != nil {
			t.Errorf("operational failure misreported as signed out: %v", err)
		}
		obs, _ := GetAuthPreflightObservation("codex", 0)
		if obs.Status != "ERROR" && obs.Status != "TIMEOUT" {
			t.Errorf("status=%s, want ERROR or TIMEOUT", obs.Status)
		}
	}
}

func TestRuntimeAuthProbeUsesWorkerCredentialEnvironment(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "codex")
	home, codexHome := t.TempDir(), t.TempDir()
	script := "#!/bin/sh\nif [ \"$HOME\" = \"$MARSHAL_TEST_HOME\" ] && [ \"$CODEX_HOME\" = \"$MARSHAL_TEST_CODEX_HOME\" ]; then echo 'Logged in using ChatGPT'; exit 0; fi\necho 'Not logged in'; exit 1\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	env := workerenv.Safe([]string{"HOME=" + home, "CODEX_HOME=" + codexHome, "MARSHAL_TEST_HOME=" + home, "MARSHAL_TEST_CODEX_HOME=" + codexHome, "GITHUB_TOKEN=excluded"}, nil)
	if err := defaultAuthPreflight(t.Context(), "codex", binary, env); err != nil {
		t.Fatal(err)
	}
	if confirmedChatAuthFailure(t.Context(), "codex", binary, env, "Not logged in · Please run /login") {
		t.Fatal("nested Claude login message attributed to signed-in Codex")
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho 'Not logged in' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if !confirmedChatAuthFailure(t.Context(), "codex", binary, env, "Not logged in") {
		t.Fatal("confirmed sign-out was ignored")
	}
}
