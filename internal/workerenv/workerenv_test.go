package workerenv

import (
	"slices"
	"strings"
	"testing"
)

// plantedEnv is an owner environment with what a worker needs and what it
// must never see.
var plantedEnv = []string{
	"PATH=/usr/bin:/bin",
	"HOME=/Users/owner",
	"USER=owner",
	"LANG=en_US.UTF-8",
	"LC_ALL=en_US.UTF-8",
	"TERM=xterm-256color",
	"TMPDIR=/var/folders/x/T/",
	"SHELL=/bin/zsh",
	"https_proxy=http://proxy:3128",
	"HTTPS_PROXY=http://proxy:3128",
	"NODE_EXTRA_CA_CERTS=/etc/ca.pem",
	"ANTHROPIC_API_KEY=sk-ant-planted",
	"CLAUDE_CONFIG_DIR=/Users/owner/.claude",
	"OPENAI_API_KEY=sk-openai-planted",
	"CODEX_HOME=/Users/owner/.codex",
	"GEMINI_API_KEY=gemini-planted",
	"GOOGLE_CLOUD_PROJECT=proj",
	"OPENCODE_CONFIG=/Users/owner/.config/opencode/opencode.json",
	"XDG_CONFIG_HOME=/Users/owner/.config",
	"MARSHAL_KNOWLEDGE_MCP=[]",
	"GOPATH=/Users/owner/go",
	// Never inherited.
	"GITHUB_TOKEN=ghp_planted",
	"GH_TOKEN=gho_planted",
	"AWS_ACCESS_KEY_ID=AKIAPLANTED",
	"AWS_SECRET_ACCESS_KEY=planted",
	"SSH_AUTH_SOCK=/private/tmp/ssh-agent.sock",
	"TMUX=/private/tmp/tmux-501/default,123,0",
	"TMUX_PANE=%3",
	"TMUX_TMPDIR=/private/tmp",
	"STRIPE_SECRET=planted",
	"NPM_TOKEN=planted",
	"SOME_SERVICE_KEY=planted",
	"MARSHAL_MASTER_KEY=planted",
	"PWD=/Users/owner/elsewhere",
	"MALFORMED",
}

func keys(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		k, _, _ := strings.Cut(e, "=")
		out = append(out, k)
	}
	return out
}

func TestSafeKeepsWhatWorkersNeed(t *testing.T) {
	got := keys(Safe(plantedEnv, nil))
	for _, want := range []string{"PATH", "HOME", "USER", "LANG", "LC_ALL", "TERM", "TMPDIR", "SHELL",
		"https_proxy", "HTTPS_PROXY", "NODE_EXTRA_CA_CERTS",
		"ANTHROPIC_API_KEY", "CLAUDE_CONFIG_DIR", "OPENAI_API_KEY", "CODEX_HOME",
		"GEMINI_API_KEY", "GOOGLE_CLOUD_PROJECT", "OPENCODE_CONFIG", "XDG_CONFIG_HOME",
		"MARSHAL_KNOWLEDGE_MCP", "GOPATH"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s was dropped; a worker needs it", want)
		}
	}
}

func TestSafeDropsEverythingElse(t *testing.T) {
	got := keys(Safe(plantedEnv, nil))
	for _, banned := range []string{"GITHUB_TOKEN", "GH_TOKEN", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
		"SSH_AUTH_SOCK", "TMUX", "TMUX_PANE", "TMUX_TMPDIR", "STRIPE_SECRET", "NPM_TOKEN",
		"SOME_SERVICE_KEY", "MARSHAL_MASTER_KEY", "PWD", "MALFORMED"} {
		if slices.Contains(got, banned) {
			t.Errorf("%s reached the worker", banned)
		}
	}
}

// A caller's extra names admit more, but never a denied variable.
func TestSafeExtraNeverAdmitsDenied(t *testing.T) {
	got := keys(Safe(plantedEnv, []string{"NPM_TOKEN", "TMUX", "GITHUB_TOKEN", "MARSHAL_MASTER_KEY"}))
	if !slices.Contains(got, "NPM_TOKEN") {
		t.Error("extra name was not admitted")
	}
	for _, denied := range []string{"TMUX", "GITHUB_TOKEN", "MARSHAL_MASTER_KEY"} {
		if slices.Contains(got, denied) {
			t.Errorf("extra name admitted denied %s", denied)
		}
	}
}

func TestSafeKeepsOrderAndValues(t *testing.T) {
	got := Safe([]string{"HOME=/h", "GITHUB_TOKEN=x", "PATH=/p=q"}, nil)
	if strings.Join(got, ";") != "HOME=/h;PATH=/p=q" {
		t.Fatalf("got %q", got)
	}
}
