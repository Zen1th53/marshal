package redaction

import (
	"math"
	"regexp"
	"strings"

	"github.com/Zen1th53/marshal/internal/auth"
)

var (
	bearerPattern         = regexp.MustCompile(`(?i)\bBearer\s+[a-zA-Z0-9_\-\.=]{10,}`)
	skPattern             = regexp.MustCompile(`(?i)\bsk-(?:proj-|ant-)?[a-zA-Z0-9_-]{16,}\b`)
	githubTokenPattern    = regexp.MustCompile(`(?i)\b(?:gh[pousr]|github_pat)_[0-9a-zA-Z_]{16,255}\b`)
	awsKeyPattern         = regexp.MustCompile(`(?i)\b(?:AKIA|ABIA|ACCA|ASIA|AKIA0)[0-9A-Z]{15,}\b`)
	slackTokenPattern     = regexp.MustCompile(`(?i)\bxox[baprs]-[0-9a-zA-Z-]{10,}\b`)
	privateKeyPattern     = regexp.MustCompile(`(?i)-----BEGIN(?: [A-Z0-9_-]+)? PRIVATE KEY-----`)
	passwordPattern       = regexp.MustCompile(`(?i)\b(?:password|passwd)\s*[:=]\s*["']?([^"'\s]+)["']?`)
	netrcPattern          = regexp.MustCompile(`(?i)\b(?:machine\s+\S+|default)[\s\S]{0,100}?\bpassword\s+\S+`)
	credentialFilePattern = regexp.MustCompile(`(?i)(\[(?:default|[a-zA-Z0-9_.-]+)\][\s\S]{0,200}?(?:aws_access_key_id|aws_secret_access_key)\s*=|\baws_secret_access_key\s*=\s*\S+|\boauth_token:\s*["']?[a-zA-Z0-9_]{16,}|\bhttps?:\/\/[^:\s]+:[^@\s]+@)`)
	googleApiKeyPattern   = regexp.MustCompile(`(?i)\bAIza[0-9A-Za-z\-_]{35}\b`)
	dbConnStringPattern   = regexp.MustCompile(`(?i)(?:postgres|postgresql|mysql|mongodb|redis|amqp|couchdb):\/\/[^:]+:[^@]+@`)
	jwtPattern            = regexp.MustCompile(`\beyJ[0-9A-Za-z_-]{8,}\.[0-9A-Za-z_-]{8,}\.[0-9A-Za-z_-]{8,}\b`)
	authorizationPattern  = regexp.MustCompile(`(?i)\bauthorization\s*:\s*(?:bearer|basic)\s+[^\s,;]{8,}`)
	sessionCookiePattern  = regexp.MustCompile(`(?i)\b(?:cookie|set-cookie)\s*:\s*[^\r\n]{8,}`)
	oauthTokenPattern     = regexp.MustCompile(`(?i)\bya29\.[0-9A-Za-z_-]{16,}\b`)
	// Sensitivity follows the key, not the length of the value. Requiring eight
	// or more characters let short secrets through in cleartext -- an audit
	// observed "password=hunter2" reaching the store unredacted. One character
	// is enough to match, while an empty value is left alone so ordinary prose
	// such as "token: " is not mangled.
	keyPattern        = regexp.MustCompile(`(?i)\b(api[_-]?key|secret|token|password|passwd|access[_-]?token|refresh[_-]?token|client[_-]?secret|authorization|auth[_-]?token|private[_-]?key|aws_secret_access_key|aws_access_key_id|oauth_token|github_token|openai_api_key|secret_key|secret_access_key|api_token|credential|credentials|app_secret|master_key|private_token|deploy_token)\s*[:=]\s*["']?([^"'\s]+)["']?`)
	genericKeyPattern = regexp.MustCompile(`(?i)\b([a-zA-Z0-9_-]*(?:secret|token|key|credential|passwd|password|auth)[a-zA-Z0-9_-]*)\s*[:=]\s*["']?([a-zA-Z0-9_\-\.\/+=]{16,})["']?`)
)

// DetectSecret checks if input contains any credential or secret pattern.
// It returns a safe, descriptive string naming the kind of secret detected,
// or empty string if no secret was detected. It never echoes the secret value.
func DetectSecret(input string) string {
	if strings.TrimSpace(input) == "" {
		return ""
	}

	if privateKeyPattern.MatchString(input) {
		return "private key pattern"
	}
	if githubTokenPattern.MatchString(input) {
		return "github token pattern"
	}
	if awsKeyPattern.MatchString(input) {
		return "aws access key pattern"
	}
	if slackTokenPattern.MatchString(input) {
		return "slack token pattern"
	}
	if skPattern.MatchString(input) {
		return "openai api key pattern"
	}
	if googleApiKeyPattern.MatchString(input) {
		return "google api key pattern"
	}
	if dbConnStringPattern.MatchString(input) {
		return "database connection uri credential"
	}
	if jwtPattern.MatchString(input) {
		return "jwt credential pattern"
	}
	if matches := authorizationPattern.FindAllString(input, -1); len(matches) > 0 {
		for _, m := range matches {
			parts := strings.Fields(m)
			if len(parts) >= 3 {
				val := strings.ToLower(strings.TrimRight(parts[len(parts)-1], ",;"))
				switch val {
				case "authorization", "authentication", "token", "header", "headers", "parser", "scheme", "format", "credential", "credentials", "request", "requests":
					continue
				}
			}
			return "authorization header credential"
		}
	}
	if matches := bearerPattern.FindAllString(input, -1); len(matches) > 0 {
		for _, m := range matches {
			if isBearerToken(m) {
				return "bearer token pattern"
			}
		}
	}
	if sessionCookiePattern.MatchString(input) {
		return "session cookie credential"
	}
	if oauthTokenPattern.MatchString(input) {
		return "oauth token pattern"
	}
	if netrcPattern.MatchString(input) {
		return ".netrc credential pattern"
	}
	if credentialFilePattern.MatchString(input) {
		return "credential file content"
	}
	if passwordPattern.MatchString(input) {
		return "explicit secret/password assignment"
	}
	if keyPattern.MatchString(input) {
		return "explicit secret/key assignment"
	}
	if matches := genericKeyPattern.FindAllStringSubmatch(input, -1); len(matches) > 0 {
		for _, m := range matches {
			if len(m) > 2 && isHighEntropyToken(m[2]) {
				return "high-entropy token next to key-like name"
			}
		}
	}

	return ""
}

func isHighEntropyToken(s string) bool {
	if len(s) < 16 {
		return false
	}
	var hasLower, hasUpper, hasDigit, hasSpecial bool
	freq := make(map[rune]int)
	for _, r := range s {
		freq[r]++
		switch {
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= '0' && r <= '9':
			hasDigit = true
		default:
			hasSpecial = true
		}
	}
	var classes int
	if hasLower {
		classes++
	}
	if hasUpper {
		classes++
	}
	if hasDigit {
		classes++
	}
	if hasSpecial {
		classes++
	}

	var entropy float64
	length := float64(len(s))
	for _, count := range freq {
		p := float64(count) / length
		entropy -= p * math.Log2(p)
	}

	if classes >= 2 && entropy >= 3.0 {
		return true
	}
	return false
}

func isBearerToken(match string) bool {
	parts := strings.Fields(match)
	if len(parts) < 2 {
		return false
	}
	token := parts[1]
	lower := strings.ToLower(token)
	switch lower {
	case "authorization", "authentication", "token", "header", "headers", "parser", "scheme", "format", "credential", "credentials", "request", "requests":
		return false
	}
	return len(token) >= 8
}

// RedactContent scrubs sensitive tokens, keys, and patterns from text before rendering in TUI.
func RedactContent(input string, knownSecrets []string) string {
	if len(knownSecrets) > 0 {
		b := auth.RedactSecrets([]byte(input), knownSecrets)
		input = string(b)
	}

	input = privateKeyPattern.ReplaceAllString(input, "[REDACTED PRIVATE KEY]")
	input = netrcPattern.ReplaceAllString(input, "[REDACTED NETRC]")
	input = credentialFilePattern.ReplaceAllString(input, "[REDACTED CREDENTIALS]")
	input = bearerPattern.ReplaceAllStringFunc(input, func(m string) string {
		if isBearerToken(m) {
			return "Bearer [REDACTED]"
		}
		return m
	})
	input = skPattern.ReplaceAllString(input, "sk-[REDACTED]")
	input = githubTokenPattern.ReplaceAllString(input, "[REDACTED]")
	input = awsKeyPattern.ReplaceAllString(input, "[REDACTED]")
	input = slackTokenPattern.ReplaceAllString(input, "[REDACTED]")
	input = keyPattern.ReplaceAllString(input, "$1: [REDACTED]")

	return input
}
