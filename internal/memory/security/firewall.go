package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrSecretDetected = errors.New("memory rejected: sensitive material or secret credential detected")
)

var (
	// Standard secret patterns
	privateKeyPattern       = regexp.MustCompile(`(?i)-----BEGIN(?: [A-Z0-9_-]+)? PRIVATE KEY-----`)
	awsKeyPattern           = regexp.MustCompile(`(?i)\b(?:AKIA|ABIA|ACCA|ASIA)[0-9A-Z]{16}\b`)
	githubTokenPattern      = regexp.MustCompile(`(?i)\b(?:ghp|gho|ghu|ghs|ghr|github_pat)_[0-9a-zA-Z_]{36,255}\b`)
	openaiKeyPattern        = regexp.MustCompile(`(?i)\bsk-[a-zA-Z0-9_-]{20,}\b`)
	slackTokenPattern       = regexp.MustCompile(`(?i)\bxox[baprs]-[0-9]{10,13}-[0-9]{10,13}-[a-zA-Z0-9]{24,34}\b`)
	googleApiKeyPattern     = regexp.MustCompile(`(?i)\bAIza[0-9A-Za-z\-_]{35}\b`)
	secretAssignmentPattern = regexp.MustCompile(`(?i)(?:api[_-]?key|password|access[_-]?token|secret_key|client_secret|auth_token)\s*[:=]\s*['"]?[^\s'"]{8,}`)
	dbConnStringPattern     = regexp.MustCompile(`(?i)(?:postgres|postgresql|mysql|mongodb|redis|amqp|couchdb):\/\/[^:]+:[^@]+@`)
)

type FirewallConfig struct {
	CanarySecrets       []string
	ForbiddenKeywords   []string
	CustomRegexPatterns []*regexp.Regexp
}

type Firewall struct {
	config FirewallConfig
}

func NewFirewall(config FirewallConfig) *Firewall {
	return &Firewall{config: config}
}

// ScanRecord inspects all fields of a MemoryRecordV2 for secret material.
// Returns ErrSecretDetected without echoing the secret content if detected.
func (f *Firewall) ScanRecord(ctx context.Context, rec model.MemoryRecordV2) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// 1. Scan title
	if reason := f.detectSecret(rec.Title); reason != "" {
		return fmt.Errorf("%w: secret detected in title (%s)", ErrSecretDetected, reason)
	}

	// 2. Scan body
	if reason := f.detectSecret(rec.Body); reason != "" {
		return fmt.Errorf("%w: secret detected in body (%s)", ErrSecretDetected, reason)
	}

	// 3. Scan Source reference
	if reason := f.detectSecret(rec.Source.Reference); reason != "" {
		return fmt.Errorf("%w: secret detected in source reference (%s)", ErrSecretDetected, reason)
	}

	// 4. Scan evidence IDs and metadata
	for _, id := range rec.EvidenceIDs {
		if reason := f.detectSecret(id); reason != "" {
			return fmt.Errorf("%w: secret detected in evidence id (%s)", ErrSecretDetected, reason)
		}
	}

	// 5. Scan ExtMeta
	if rec.ExtMeta != nil {
		metaBytes, err := json.Marshal(rec.ExtMeta)
		if err == nil {
			if reason := f.detectSecret(string(metaBytes)); reason != "" {
				return fmt.Errorf("%w: secret detected in metadata (%s)", ErrSecretDetected, reason)
			}
		}
	}

	return nil
}

// ScanText inspects a raw string for sensitive patterns.
func (f *Firewall) ScanText(text string) error {
	if reason := f.detectSecret(text); reason != "" {
		return fmt.Errorf("%w: %s", ErrSecretDetected, reason)
	}
	return nil
}

func (f *Firewall) detectSecret(text string) string {
	if text == "" {
		return ""
	}

	// 1. Check custom canaries and configured literals
	for _, canary := range f.config.CanarySecrets {
		if canary != "" && strings.Contains(text, canary) {
			return "configured canary secret match"
		}
	}

	for _, kw := range f.config.ForbiddenKeywords {
		if kw != "" && strings.Contains(strings.ToLower(text), strings.ToLower(kw)) {
			return "configured sensitive keyword match"
		}
	}

	// 2. Standard pattern checks
	if privateKeyPattern.MatchString(text) {
		return "private key pattern"
	}
	if githubTokenPattern.MatchString(text) {
		return "github token pattern"
	}
	if awsKeyPattern.MatchString(text) {
		return "aws access key pattern"
	}
	if openaiKeyPattern.MatchString(text) {
		return "openai api key pattern"
	}
	if slackTokenPattern.MatchString(text) {
		return "slack token pattern"
	}
	if googleApiKeyPattern.MatchString(text) {
		return "google api key pattern"
	}
	if dbConnStringPattern.MatchString(text) {
		return "database connection uri credential"
	}
	if secretAssignmentPattern.MatchString(text) {
		return "explicit secret/password assignment"
	}

	// 3. Custom regex patterns
	for _, reg := range f.config.CustomRegexPatterns {
		if reg != nil && reg.MatchString(text) {
			return "custom security regex match"
		}
	}

	return ""
}
