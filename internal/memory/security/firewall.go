package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/redaction"
)

var (
	ErrSecretDetected = errors.New("memory rejected: sensitive material or secret credential detected")
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

// ScanRecord inspects supplied content fields of a MemoryRecordV2 for secret material.
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

	// Runtime identifiers are structured data, not assignments supplied in prose.
	// Retain credential-shape checks for malformed/imported identifiers.
	for _, id := range append([]string{rec.ScopeID, rec.BranchName, rec.WorktreeID, rec.ACLScope}, rec.EvidenceIDs...) {
		if reason := redaction.DetectCredentialShape(id); reason != "" {
			return fmt.Errorf("%w: credential in identifier (%s)", ErrSecretDetected, reason)
		}
	}

	// 6. Scan ExtMeta
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

	// 2. Custom regex patterns
	for _, reg := range f.config.CustomRegexPatterns {
		if reg != nil && reg.MatchString(text) {
			return "custom security regex match"
		}
	}

	// 3. Central repository detector from redaction
	if kind := redaction.DetectSecret(text); kind != "" {
		return kind
	}

	return ""
}
