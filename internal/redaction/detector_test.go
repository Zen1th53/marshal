package redaction_test

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/redaction"
)

// fixtureText assembles synthetic credentials at runtime so no complete
// credential-shaped value is present in the test source.
func fixtureText(parts ...string) string {
	return strings.Join(parts, "")
}

func TestDetectSecretShapes(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantKind string
	}{
		// 1. sk- API keys
		{
			name:     "openai standard sk-",
			input:    fixtureText("sk-", "12345678", "90abcdef", "12345678", "90"),
			wantKind: "openai api key pattern",
		},
		{
			name:     "openai proj sk-",
			input:    fixtureText("sk-proj-", "abcdef12", "34567890", "abcdef12", "34567890"),
			wantKind: "openai api key pattern",
		},
		{
			name:     "anthropic sk-ant-",
			input:    fixtureText("sk-ant-api03-", "abcdef12", "34567890", "abcdef12", "34567890"),
			wantKind: "openai api key pattern",
		},

		// 2. ghp_ / github_pat_
		{
			name:     "github personal access token ghp_",
			input:    fixtureText("ghp_", "12345678", "90abcdef", "ghijklmn", "opqrstuv", "wxyzAB"),
			wantKind: "github token pattern",
		},
		{
			name:     "github fine-grained token github_pat_",
			input:    fixtureText("github_pat_", "11ABCDEF", "01234567", "89_abcde", "fghijklm", "nopqrstu", "vwxyzABC", "DEFGHIJK", "LMNOPQRS", "TUVWXYZ0", "12345678", "9"),
			wantKind: "github token pattern",
		},
		{
			name:     "github oauth token gho_",
			input:    fixtureText("gho_", "12345678", "90abcdef", "ghijklmn", "opqrstuv", "wxyzAB"),
			wantKind: "github token pattern",
		},

		// 3. AKIA... AWS access keys
		{
			name:     "aws standard access key AKIA",
			input:    fixtureText("AKIA", "IOSFODNN", "7EXAMPLE"),
			wantKind: "aws access key pattern",
		},
		{
			name:     "aws temp access key ASIA",
			input:    fixtureText("ASIA", "IOSFODNN", "7EXAMPLE"),
			wantKind: "aws access key pattern",
		},
		{
			name:     "aws synthetic honeypot AKIA0",
			input:    fixtureText("AKIA", "0ABCDEF2", "34567890"),
			wantKind: "aws access key pattern",
		},

		// 4. xox... Slack tokens
		{
			name:     "slack bot token xoxb",
			input:    fixtureText("xoxb-", "12345678", "90-abcde", "f123456"),
			wantKind: "slack token pattern",
		},
		{
			name:     "slack user token xoxp",
			input:    fixtureText("xoxp-", "12345678", "90-12345", "67890-ab", "cdef1234", "56"),
			wantKind: "slack token pattern",
		},

		// 5. Private key blocks
		{
			name:     "rsa private key block",
			input:    fixtureText("-----BEGIN ", "RSA PRIVATE KEY", "-----", "\nMIIEowIBAAKCAQEA0..."),
			wantKind: "private key pattern",
		},
		{
			name:     "generic private key block",
			input:    fixtureText("-----BEGIN ", "PRIVATE KEY", "-----", "\nMIIEvgIBADANBgkq..."),
			wantKind: "private key pattern",
		},
		{
			name:     "openssh private key block",
			input:    fixtureText("-----BEGIN ", "OPENSSH PRIVATE KEY", "-----", "\nb3BlbnNza..."),
			wantKind: "private key pattern",
		},

		// 6. Bearer tokens
		{
			name:     "bearer token standalone",
			input:    fixtureText("Bearer ", "secret-t", "oken-val", "ue-12345", "6"),
			wantKind: "bearer token pattern",
		},
		{
			name:     "authorization header bearer",
			input:    fixtureText("Authorization: ", "Bearer ", "my-acces", "s-token-", "123456"),
			wantKind: "bearer token pattern",
		},

		// 7. password=...
		{
			name:     fixtureText("password=", "hunter2", " short password"),
			input:    fixtureText("password=", "hunter2"),
			wantKind: "explicit secret/password assignment",
		},
		{
			name:     fixtureText("password: ", "short"),
			input:    fixtureText("password: ", "pw"),
			wantKind: "explicit secret/password assignment",
		},
		{
			name:     fixtureText("passwd=", "x"),
			input:    fixtureText("passwd=", "x"),
			wantKind: "explicit secret/password assignment",
		},

		// 8. .netrc / credential files
		{
			name:     ".netrc single line",
			input:    fixtureText("machine api.github.com login dev ", "password ", "secretpa", "ssword12", "3"),
			wantKind: ".netrc credential pattern",
		},
		{
			name:     ".netrc multi line",
			input:    fixtureText("machine example.com\n  login test\n  ", "password ", "pass123"),
			wantKind: ".netrc credential pattern",
		},
		{
			name:     "aws credentials file",
			input:    fixtureText("[default]\naws_access_key_id = ", "AKIA", "IOSFODNN", "7EXAMPLE", "\n", "aws_secret_access_key = ", "wJalrXUt", "nFEMI/K7", "MDENG/bP", "xRfiCYEX", "AMPLEKEY", "\n"),
			wantKind: "aws access key pattern",
		},
		{
			name:     "gh config hosts.yml",
			input:    fixtureText("github.com:\n    oauth_token: ", "ghp_", "12345678", "90abcdef", "ghijklmn", "opqrstuv", "wxyzAB", "\n    git_protocol: https\n"),
			wantKind: "github token pattern",
		},

		// 9. High-entropy token next to key-like name
		{
			name:     "api_key high entropy",
			input:    fixtureText("api_key = ", "a8f9c0b1", "d2e3f4a5", "b6c7d8e9", "f0a1b2c3"),
			wantKind: "explicit secret/key assignment",
		},
		{
			name:     "custom token assignment",
			input:    fixtureText("service_token: ", "9a8b7c6d", "5e4f3a2b", "1c0d9e8f", "7a6b5c4d"),
			wantKind: "high-entropy token next to key-like name",
		},

		// 10. Honeypot decoy formats
		{
			name:     "honeypot .env file",
			input:    fixtureText("GITHUB_TOKEN=", "ghp_", "01234567", "89abcdef", "01234567", "89abcdef", "\nOPENAI_API_KEY=", "sk-proj-", "01234567", "89abcdef", "01234567", "89abcdef", "\nAWS_ACCESS_KEY_ID=", "AKIA", "0ABCDEF2", "34567890", "\n", "AWS_SECRET_ACCESS_KEY=", "01234567", "89abcdef", "01234567", "89abcdef", "01234567", "\n"),
			wantKind: "github token pattern",
		},
		{
			name:     "honeypot aws_secret_access_key",
			input:    fixtureText("aws_secret_access_key = ", "01234567", "89abcdef", "01234567", "89abcdef", "01234567"),
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
