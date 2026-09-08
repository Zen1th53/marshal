package execution

import (
	"os"
	"strings"
)

// DefaultAllowedEnvVars are system variables safe to inherit by child workers.
var DefaultAllowedEnvVars = []string{
	"PATH",
	"HOME",
	"USER",
	"LANG",
	"LC_ALL",
	"TERM",
	"SHELL",
	"TMPDIR",
	"GOROOT",
	"GOPATH",
}

// ComputeSafeEnvironment builds a minimal, scrubbed environment for child processes.
func ComputeSafeEnvironment(inherited []string, extraAllowed []string, taskSecrets map[string]string) []string {
	if inherited == nil {
		inherited = os.Environ()
	}

	allowedSet := make(map[string]bool)
	for _, k := range DefaultAllowedEnvVars {
		allowedSet[k] = true
	}
	for _, k := range extraAllowed {
		allowedSet[k] = true
	}

	var safeEnv []string
	for _, env := range inherited {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		k := parts[0]
		// Don't inherit sensitive keys unless explicitly allowed
		if allowedSet[k] && !isDangerousKey(k) {
			safeEnv = append(safeEnv, env)
		}
	}

	// Add task-specific scoped secrets
	for k, v := range taskSecrets {
		if k != "" && v != "" {
			safeEnv = append(safeEnv, k+"="+v)
		}
	}

	return safeEnv
}

func isDangerousKey(key string) bool {
	upper := strings.ToUpper(key)
	// Block automatic inheritance of sensitive credentials
	sensitivePrefixes := []string{"MARSHAL_MASTER_", "AWS_SECRET", "GITHUB_TOKEN", "SSH_PRIVATE", "DOCKER_AUTH"}
	for _, p := range sensitivePrefixes {
		if strings.HasPrefix(upper, p) {
			return true
		}
	}
	return false
}

// SanitizeProcessArgs scans command line arguments and flags for exposed secrets.
func SanitizeProcessArgs(args []string) []string {
	sanitized := make([]string, len(args))
	for i, arg := range args {
		lower := strings.ToLower(arg)
		if strings.Contains(lower, "token=") || strings.Contains(lower, "password=") || strings.Contains(lower, "secret=") || strings.Contains(lower, "key=") {
			parts := strings.SplitN(arg, "=", 2)
			sanitized[i] = parts[0] + "=[REDACTED]"
		} else {
			sanitized[i] = RedactSecrets(arg)
		}
	}
	return sanitized
}
