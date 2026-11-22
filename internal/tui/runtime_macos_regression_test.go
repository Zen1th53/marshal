package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/workerenv"
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

func TestAuthRuntimeProviderEvidence(t *testing.T) {
	// A separate status probe may succeed even though the interactive session
	// needs login or cannot refresh its token. It must not veto direct evidence.
	binary := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho 'Logged in using ChatGPT'\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	prompt := "╭──────────────────────────────╮\n│ ❯ 1. Sign in with ChatGPT    │\n│   2. Sign in with Device Code│\n│   3. Provide your own API key│\n╰──────────────────────────────╯\n"
	refreshLoop := "ERROR: Failed to refresh token: 401 Unauthorized: Your refresh token was revoked.\nReconnecting... 2/5\n"
	for _, tc := range []struct {
		name, provider, text string
		want                 bool
	}{
		{"own_login_prompt", "codex", prompt, true},
		{"own_styled_login_prompt", "codex", "\x1b[31m" + prompt + "\x1b[0m", true},
		{"own_refresh_loop", "codex", refreshLoop, true},
		{"own_auth_exit", "codex", "Error: Failed to refresh token: refresh token was revoked\n", true},
		{"nested_claude_login", "codex", "Not logged in · Please run /login", false},
		{"nested_opencode_login", "codex", "Unauthorized: run opencode auth login", false},
		{"other_provider_prompt", "claude", prompt, false},
		{"other_provider_refresh_loop", "claude", refreshLoop, false},
		{"own_claude_login", "claude", "Not logged in · Please run /login", true},
		{"quoted_prompt", "codex", "example: `Sign in with ChatGPT` using an API key", false},
		{"quoted_refresh_error", "codex", "example: `Failed to refresh token: refresh token was revoked`", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := confirmedChatAuthFailure(t.Context(), tc.provider, binary, nil, tc.text); got != tc.want {
				t.Fatalf("confirmedChatAuthFailure(%q, %q) = %v, want %v", tc.provider, tc.text, got, tc.want)
			}
		})
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
