package evidence

import (
	"context"
	"strings"
)

// Sanitizer is the evidence persistence boundary for metadata that could
// otherwise expose sensitive material. Runtime systems may compose additional
// implementations without making T06 depend on them.
type Sanitizer interface {
	SanitizeNode(context.Context, Node) (Node, error)
}

// SanitizerConfig supplies explicit local policy without attempting global
// secret discovery.
type SanitizerConfig struct {
	SensitiveKeys     []string
	LiteralSecrets    []string
	MaxMetadataKeyLen int
	MaxMetadataValLen int
	MaxMetadataFields int
}

// StrictSanitizer rejects unsafe metadata and returns a detached copy of safe
// metadata. It never silently falls back to unsanitized persistence.
type StrictSanitizer struct{ config SanitizerConfig }

func NewStrictSanitizer(config SanitizerConfig) *StrictSanitizer {
	if config.MaxMetadataKeyLen == 0 {
		config.MaxMetadataKeyLen = 256
	}
	if config.MaxMetadataValLen == 0 {
		config.MaxMetadataValLen = 4096
	}
	if config.MaxMetadataFields == 0 {
		config.MaxMetadataFields = 1024
	}
	return &StrictSanitizer{config: config}
}

func (s *StrictSanitizer) SanitizeNode(ctx context.Context, node Node) (Node, error) {
	if err := ctx.Err(); err != nil {
		return Node{}, NewError(CodeSecretRejected, err)
	}
	clean := CloneNode(node)
	if len(clean.Metadata) > s.config.MaxMetadataFields {
		return Node{}, ErrSecretRejected
	}
	for key, value := range clean.Metadata {
		if len(key) > s.config.MaxMetadataKeyLen || len(value) > s.config.MaxMetadataValLen ||
			sensitiveKey(key, s.config.SensitiveKeys) || containsLiteral(value, s.config.LiteralSecrets) {
			return Node{}, ErrSecretRejected
		}
	}
	return clean, nil
}

func sensitiveKey(key string, configured []string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, forbidden := range append([]string{"authorization", "password", "private_key", "secret", "token"}, configured...) {
		if normalized == strings.ToLower(strings.TrimSpace(forbidden)) {
			return true
		}
	}
	return false
}

func containsLiteral(value string, literals []string) bool {
	for _, literal := range literals {
		if literal != "" && strings.Contains(value, literal) {
			return true
		}
	}
	return false
}
