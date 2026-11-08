package policy

import (
	"bytes"
	"regexp"
)

// Redactor is the redaction hook applied to every captured visible file
// before hashing, analysis, search or rendering. Snapshot hashes are taken
// over the redacted bytes, so callers never see a hash that would confirm
// a guessed secret.
//
// Implementations must be deterministic pure functions of (path, data) and
// should preserve length and line structure so byte spans stay meaningful.
// Name and Version are part of the policy identity.
type Redactor interface {
	Name() string
	Version() string
	// Redact returns the redacted bytes and the number of redactions. It
	// must not modify data in place.
	Redact(path string, data []byte) ([]byte, int)
}

// NoRedactor returns data unchanged.
type NoRedactor struct{}

func (NoRedactor) Name() string    { return "none" }
func (NoRedactor) Version() string { return "1" }
func (NoRedactor) Redact(_ string, data []byte) ([]byte, int) {
	return data, 0
}

type secretRule struct {
	re    *regexp.Regexp
	group int // submatch to mask; 0 = whole match
	// any lists literals one of which must occur (in the lowercased input
	// when fold is set) for the rule to possibly match; a cheap prefilter.
	any  []string
	fold bool
}

type secretRedactor struct{ rules []secretRule }

// DefaultRedactor masks common credential formats with '*' bytes,
// preserving length and newlines.
func DefaultRedactor() Redactor {
	return &secretRedactor{rules: []secretRule{
		// PEM private key bodies (markers kept so the structure stays visible).
		{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]{0,40}PRIVATE KEY-----(.*?)-----END [A-Z ]{0,40}PRIVATE KEY-----`), 1, []string{"PRIVATE KEY-----"}, false},
		{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), 0, []string{"AKIA"}, false},
		{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,255}\b`), 0, []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_"}, false},
		{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{40,255}\b`), 0, []string{"github_pat_"}, false},
		{regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,200}\b`), 0, []string{"xox"}, false},
		{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,200}\b`), 0, []string{"sk-"}, false},
		{regexp.MustCompile(`(?i)\b(?:password|passwd|secret|api[_-]?key|access[_-]?token|auth[_-]?token)\b["']?\s*[:=]\s*["']([^"'\n]{8,200})["']`), 1,
			[]string{"password", "passwd", "secret", "apikey", "api_key", "api-key", "token"}, true},
	}}
}

func (*secretRedactor) Name() string    { return "secrets" }
func (*secretRedactor) Version() string { return "1" }

func (r *secretRedactor) Redact(_ string, data []byte) ([]byte, int) {
	var out []byte
	var lower []byte
	n := 0
	for _, rule := range r.rules {
		hay := data
		if rule.fold {
			if lower == nil {
				lower = bytes.ToLower(data)
			}
			hay = lower
		}
		hit := false
		for _, lit := range rule.any {
			if bytes.Contains(hay, []byte(lit)) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		src := data
		if out != nil {
			src = out
		}
		locs := rule.re.FindAllSubmatchIndex(src, -1)
		if len(locs) == 0 {
			continue
		}
		if out == nil {
			out = bytes.Clone(data)
		}
		for _, loc := range locs {
			s, e := loc[2*rule.group], loc[2*rule.group+1]
			if s < 0 {
				continue
			}
			for i := s; i < e; i++ {
				if out[i] != '\n' && out[i] != '\r' {
					out[i] = '*'
				}
			}
			n++
		}
	}
	if out == nil {
		return data, 0
	}
	return out, n
}
