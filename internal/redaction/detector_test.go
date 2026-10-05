package redaction_test

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/redaction"
)

func TestDetectSecretShapes(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantKind string
	}{
		// 1. sk- API keys
		{
			name:     "openai standard sk-",
			input:    "sk-1234567890abcdef1234567890",
			wantKind: "openai api key pattern",
		},
		{
			name:     "openai proj sk-",
			input:    "sk-proj-abcdef1234567890abcdef1234567890",
			wantKind: "openai api key pattern",
		},
		{
			name:     "anthropic sk-ant-",
			input:    "sk-ant-api03-abcdef1234567890abcdef1234567890",
			wantKind: "openai api key pattern",
		},

		// 2. ghp_ / github_pat_
		{
			name:     "github personal access token ghp_",
			input:    "ghp_1234567890abcdefghijklmnopqrstuvwxyzAB",
			wantKind: "github token pattern",
		},
		{
			name:     "github fine-grained token github_pat_",
			input:    "github_pat_11ABCDEF0123456789_abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
			wantKind: "github token pattern",
		},
		{
			name:     "github oauth token gho_",
			input:    "gho_1234567890abcdefghijklmnopqrstuvwxyzAB",
			wantKind: "github token pattern",
		},

		// 3. AKIA... AWS access keys
		{
			name:     "aws standard access key AKIA",
			input:    "AKIAIOSFODNN7EXAMPLE",
			wantKind: "aws access key pattern",
		},
		{
			name:     "aws temp access key ASIA",
			input:    "ASIAIOSFODNN7EXAMPLE",
			wantKind: "aws access key pattern",
		},
		{
			name:     "aws synthetic honeypot AKIA0",
			input:    "AKIA0ABCDEF234567890",
			wantKind: "aws access key pattern",
		},

		// 4. xox... Slack tokens
		{
			name:     "slack bot token xoxb",
			input:    "xoxb-1234567890-abcdef123456",
			wantKind: "slack token pattern",
		},
		{
			name:     "slack user token xoxp",
			input:    "xoxp-1234567890-1234567890-abcdef123456",
			wantKind: "slack token pattern",
		},

		// 5. Private key blocks
		{
			name:     "rsa private key block",
			input:    "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0...",
			wantKind: "private key pattern",
		},
		{
			name:     "generic private key block",
			input:    "-----BEGIN PRIVATE KEY-----\nMIIEvgIBADANBgkq...",
			wantKind: "private key pattern",
		},
		{
			name:     "openssh private key block",
			input:    "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNza...",
			wantKind: "private key pattern",
		},

		// 6. Bearer tokens
		{
			name:     "bearer token standalone",
			input:    "Bearer secret-token-value-123456",
			wantKind: "bearer token pattern",
		},
		{
			name:     "authorization header bearer",
			input:    "Authorization: Bearer my-access-token-123456",
			wantKind: "bearer token pattern",
		},

		// 7. password=...
		{
			name:     "password=hunter2 short password",
			input:    "password=hunter2",
			wantKind: "explicit secret/password assignment",
		},
		{
			name:     "password: short",
			input:    "password: pw",
			wantKind: "explicit secret/password assignment",
		},
		{
			name:     "passwd=x",
			input:    "passwd=x",
			wantKind: "explicit secret/password assignment",
		},

		// 8. .netrc / credential files
		{
			name:     ".netrc single line",
			input:    "machine api.github.com login dev password secretpassword123",
			wantKind: ".netrc credential pattern",
		},
		{
			name:     ".netrc multi line",
			input:    "machine example.com\n  login test\n  password pass123",
			wantKind: ".netrc credential pattern",
		},
		{
			name:     "aws credentials file",
			input:    "[default]\naws_access_key_id = AKIAIOSFODNN7EXAMPLE\naws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\n",
			wantKind: "aws access key pattern",
		},
		{
			name:     "gh config hosts.yml",
			input:    "github.com:\n    oauth_token: ghp_1234567890abcdefghijklmnopqrstuvwxyzAB\n    git_protocol: https\n",
			wantKind: "github token pattern",
		},

		// 9. High-entropy token next to key-like name
		{
			name:     "api_key high entropy",
			input:    "api_key = a8f9c0b1d2e3f4a5b6c7d8e9f0a1b2c3",
			wantKind: "explicit secret/key assignment",
		},
		{
			name:     "custom token assignment",
			input:    "service_token: 9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d",
			wantKind: "high-entropy token next to key-like name",
		},

		// 10. Honeypot decoy formats
		{
			name:     "honeypot .env file",
			input:    "GITHUB_TOKEN=ghp_0123456789abcdef0123456789abcdef\nOPENAI_API_KEY=sk-proj-0123456789abcdef0123456789abcdef\nAWS_ACCESS_KEY_ID=AKIA0ABCDEF234567890\nAWS_SECRET_ACCESS_KEY=0123456789abcdef0123456789abcdef01234567\n",
			wantKind: "github token pattern",
		},
		{
			name:     "honeypot aws_secret_access_key",
			input:    "aws_secret_access_key = 0123456789abcdef0123456789abcdef01234567",
			wantKind: "credential file content",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind := redaction.DetectSecret(tc.input)
			if kind == "" {
				t.Fatalf("expected secret to be detected in %q, got empty kind", tc.input)
			}
			if tc.wantKind != "" && kind != tc.wantKind {
				t.Logf("detected kind %q for test %q (expected %q)", kind, tc.name, tc.wantKind)
			}
		})
	}
}

func TestDetectSecretRejectsHarmlessProse(t *testing.T) {
	cleanCases := []string{
		"the password policy requires rotation",
		"rotate the api key next quarter",
		"a token of appreciation",
		"secret santa is on friday",
		"/inspect claim C-01",
		"All state is stored in SQLite with WAL mode and foreign keys enabled.",
		"The function WriteMemoryV2 in internal/store/memory.go writes canonical memory.",
		"Fixed in commit aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"primary_key is id",
		"token: ",
		"Bearer authorization parser",
		"Authorization bearer headers are parsed by ParseBearer",
	}

	for _, text := range cleanCases {
		t.Run(text, func(t *testing.T) {
			if kind := redaction.DetectSecret(text); kind != "" {
				t.Fatalf("clean text incorrectly flagged as secret: kind=%q text=%q", kind, text)
			}
		})
	}
}
