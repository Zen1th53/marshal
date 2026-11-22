package execution

import (
	"strings"
	"testing"
)

func TestCredentials_ComputeSafeEnvironment(t *testing.T) {
	inherited := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/user",
		"USER=runner",
		"MARSHAL_MASTER_KEY=supersecretkey12345",
		"AWS_SECRET_ACCESS_KEY=AKIAIOSFODNN7EXAMPLESECRET",
		"GITHUB_TOKEN=ghp_1234567890abcdef1234567890",
		"UNKNOWN_VAR=dont_leak_me",
	}

	extraAllowed := []string{"EXTRA_VAR"}
	taskSecrets := map[string]string{
		"TASK_AUTH_TOKEN": "task-token-12345",
	}

	safeEnv := ComputeSafeEnvironment(inherited, extraAllowed, taskSecrets)

	envMap := make(map[string]string)
	for _, env := range safeEnv {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	// Safe variables preserved
	if envMap["PATH"] != "/usr/bin:/bin" {
		t.Fatalf("expected PATH preserved, got %q", envMap["PATH"])
	}
	if envMap["HOME"] != "/home/user" {
		t.Fatalf("expected HOME preserved, got %q", envMap["HOME"])
	}

	// Dangerous credentials stripped
	if _, exists := envMap["MARSHAL_MASTER_KEY"]; exists {
		t.Fatalf("MARSHAL_MASTER_KEY leaked into safe environment!")
	}
	if _, exists := envMap["AWS_SECRET_ACCESS_KEY"]; exists {
		t.Fatalf("AWS_SECRET_ACCESS_KEY leaked into safe environment!")
	}
	if _, exists := envMap["GITHUB_TOKEN"]; exists {
		t.Fatalf("GITHUB_TOKEN leaked into safe environment!")
	}
	if _, exists := envMap["UNKNOWN_VAR"]; exists {
		t.Fatalf("UNKNOWN_VAR leaked into safe environment!")
	}

	// Injected task scoped secret present
	if envMap["TASK_AUTH_TOKEN"] != "task-token-12345" {
		t.Fatalf("expected TASK_AUTH_TOKEN present, got %q", envMap["TASK_AUTH_TOKEN"])
	}
}
