// Package workerenv decides which of the owner's environment variables a
// provider worker running on the host may inherit.
//
// A host worker (claude, codex, agy, opencode) used to start with the
// owner's whole environment: cloud keys, GITHUB_TOKEN, SSH_AUTH_SOCK and the
// tmux client variables that let a process drive the owner's Board through
// the tmux socket. Safe keeps only what a provider CLI needs to run and
// sign in: the basic process environment, locale, proxy and CA settings,
// the Go toolchain, git identity, each provider's own auth and config
// variables, and Marshal's own MARSHAL_* worker variables. Everything else
// is dropped.
package workerenv

import "strings"

// allowedKeys are inherited by exact name.
var allowedKeys = []string{
	// Process basics. The CLIs read their config dirs under HOME.
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TZ",
	"LANG", "LANGUAGE", "TERM", "COLORTERM", "NO_COLOR",
	"__CF_USER_TEXT_ENCODING",
	// Proxies, both spellings, and the CA bundles behind them.
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY",
	"http_proxy", "https_proxy", "no_proxy", "all_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE",
	// The Go toolchain a worker builds and tests with.
	"GOROOT", "GOPATH", "GOCACHE", "GOMODCACHE", "GOTOOLCHAIN", "GOFLAGS",
	"GOPROXY", "GOSUMDB", "GOPRIVATE", "GONOSUMDB", "GONOPROXY",
	// Git identity and config location for the worker's commits.
	"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL",
	"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_NOSYSTEM",
	// Other model vendors opencode can be signed in to.
	"DEEPSEEK_API_KEY", "OLLAMA_API_KEY", "OLLAMA_HOST",
	// Claude on Vertex.
	"CLOUD_ML_REGION",
}

// allowedPrefixes are inherited by name prefix.
var allowedPrefixes = []string{
	"LC_",                   // locale
	"XDG_",                  // opencode keeps its config and auth under the XDG dirs
	"ANTHROPIC_", "CLAUDE_", // claude
	"OPENAI_", "CODEX_", // codex
	"GEMINI_", "GOOGLE_", "AGY_", "ANTIGRAVITY_", // agy
	"OPENCODE_", // opencode
	"MARSHAL_",  // Marshal's own worker variables
}

// deniedPrefixes are never inherited, even when an allow rule or a caller's
// extra key would admit them.
var deniedPrefixes = []string{
	// The tmux client variables: with them a worker could drive the owner's
	// Board through the tmux socket.
	"TMUX",
	// Long-lived credentials that no worker needs.
	"MARSHAL_MASTER_", "AWS_SECRET", "GITHUB_TOKEN", "SSH_PRIVATE", "DOCKER_AUTH",
}

// Safe returns the entries of inherited that a host worker may keep, in
// their original order. extra admits further exact names; it never admits a
// denied one.
func Safe(inherited, extra []string) []string {
	out := make([]string, 0, len(inherited))
	for _, entry := range inherited {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		if Allowed(key, extra) {
			out = append(out, entry)
		}
	}
	return out
}

// Allowed reports whether a host worker may inherit the variable key.
func Allowed(key string, extra []string) bool {
	upper := strings.ToUpper(key)
	for _, p := range deniedPrefixes {
		if strings.HasPrefix(upper, p) {
			return false
		}
	}
	for _, k := range allowedKeys {
		if key == k {
			return true
		}
	}
	for _, k := range extra {
		if key == k {
			return true
		}
	}
	for _, p := range allowedPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}
