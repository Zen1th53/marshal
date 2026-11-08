// Package policy defines what a caller may see: allow/deny globs over
// repository paths, a per-file size cap and a redaction hook. Every output
// produced under a policy must be a function of the policy-visible snapshot
// only (noninterference, SPEC §1).
package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
)

// FormatVersion is mixed into policy identifiers.
const FormatVersion = "cec/policy/v1"

// DefaultMaxFileBytes caps how many bytes of a single file are captured.
// Larger files are listed as skipped; their content never enters outputs.
const DefaultMaxFileBytes = 2 << 20

// DefaultDeny lists paths that are never visible unless a policy replaces
// the deny list. It targets credentials and key material.
var DefaultDeny = []string{
	".git/**",
	".env", ".env.*", "*.env",
	"*.pem", "*.key", "*.p12", "*.pfx", "*.jks", "*.keystore", "*.kdbx",
	"id_rsa*", "id_dsa*", "id_ecdsa*", "id_ed25519*",
	".netrc", ".npmrc", ".pypirc", ".pgpass", ".htpasswd",
	"credentials", "credentials.json", "*.credentials.json",
	"**/secrets/**", "**/.secrets/**",
	"*.tfstate", "*.tfstate.*",
}

// Spec is the serializable description of a policy.
type Spec struct {
	// PathRule identifies the host predicate version in the policy identity.
	PathRule string            `json:"path_rule,omitempty"`
	DenyPath func(string) bool `json:"-"`
	// Name is a human label; it is not part of the identity.
	Name string `json:"name,omitempty"`
	// Allow lists visible paths. Empty means everything ("**").
	Allow []string `json:"allow,omitempty"`
	// Deny lists hidden paths; deny wins over allow. Nil means DefaultDeny;
	// an explicit empty slice means no deny rules.
	Deny []string `json:"deny"`
	// MaxFileBytes caps captured bytes per file (0 = DefaultMaxFileBytes).
	MaxFileBytes int64 `json:"max_file_bytes,omitempty"`
}

// Policy is a compiled, immutable policy.
type Policy struct {
	spec     Spec
	allow    []Glob
	deny     []Glob
	redactor Redactor
	id       string

	// memo caches Visible results (a pure function of the path).
	memo  sync.Map
	memoN atomic.Int64
}

// maxMemo bounds the visibility memo.
const maxMemo = 1 << 20

// New compiles spec with the given redactor (nil = DefaultRedactor()).
func New(spec Spec, r Redactor) (*Policy, error) {
	if (spec.PathRule == "") != (spec.DenyPath == nil) {
		return nil, fmt.Errorf("policy: path rule needs both identity and predicate")
	}
	if spec.Deny == nil {
		spec.Deny = slices.Clone(DefaultDeny)
	}
	if len(spec.Allow) == 0 {
		spec.Allow = []string{"**"}
	}
	if spec.MaxFileBytes <= 0 {
		spec.MaxFileBytes = DefaultMaxFileBytes
	}
	if r == nil {
		r = DefaultRedactor()
	}
	spec.Allow = sortedUnique(spec.Allow)
	spec.Deny = sortedUnique(spec.Deny)
	p := &Policy{spec: spec, redactor: r}
	for _, a := range spec.Allow {
		g, err := CompileGlob(a)
		if err != nil {
			return nil, err
		}
		p.allow = append(p.allow, g)
	}
	for _, d := range spec.Deny {
		g, err := CompileGlob(d)
		if err != nil {
			return nil, err
		}
		p.deny = append(p.deny, g)
	}
	p.id = computeID(spec, r)
	return p, nil
}

// Default returns the default policy (allow all, DefaultDeny, default redactor).
func Default() *Policy {
	p, err := New(Spec{Name: "default"}, nil)
	if err != nil {
		panic(err)
	}
	return p
}

func sortedUnique(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

func computeID(spec Spec, r Redactor) string {
	doc := struct {
		Format       string   `json:"format"`
		Allow        []string `json:"allow"`
		Deny         []string `json:"deny"`
		MaxFileBytes int64    `json:"max_file_bytes"`
		Redactor     string   `json:"redactor"`
		PathRule     string   `json:"path_rule,omitempty"`
	}{FormatVersion, spec.Allow, spec.Deny, spec.MaxFileBytes, r.Name() + "@" + r.Version(), spec.PathRule}
	b, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(b)
	return "pol_" + hex.EncodeToString(sum[:16])
}

// ID is H(scope, redaction rules, detector versions).
func (p *Policy) ID() string { return p.id }

// Spec returns the normalized spec.
func (p *Policy) Spec() Spec {
	s := p.spec
	s.Allow = slices.Clone(s.Allow)
	s.Deny = slices.Clone(s.Deny)
	return s
}

// MaxFileBytes returns the per-file capture cap.
func (p *Policy) MaxFileBytes() int64 { return p.spec.MaxFileBytes }

// Redactor returns the redaction hook.
func (p *Policy) Redactor() Redactor { return p.redactor }

// RedactorVersion returns "name@version" of the redactor.
func (p *Policy) RedactorVersion() string { return p.redactor.Name() + "@" + p.redactor.Version() }

// Visible reports whether the relative slash path is visible.
func (p *Policy) Visible(rel string) bool {
	if v, ok := p.memo.Load(rel); ok {
		return v.(bool)
	}
	v := p.visible(rel)
	if p.memoN.Load() < maxMemo {
		if _, loaded := p.memo.LoadOrStore(rel, v); !loaded {
			p.memoN.Add(1)
		}
	}
	return v
}

func (p *Policy) visible(rel string) bool {
	if p.spec.DenyPath != nil && p.spec.DenyPath(rel) {
		return false
	}
	ok := false
	for _, g := range p.allow {
		if g.Match(rel) {
			ok = true
			break
		}
	}
	if !ok {
		return false
	}
	for _, g := range p.deny {
		if g.Match(rel) {
			return false
		}
	}
	return true
}

// DirMayContainVisible reports whether a directory could contain visible
// files. It is used only to prune walks; it is conservative (true unless a
// deny rule of the form "<prefix>/**" covers the directory).
func (p *Policy) DirMayContainVisible(relDir string) bool {
	probe := relDir + "/\x00probe"
	for _, g := range p.deny {
		if n := len(g.segs); n > 0 && g.segs[n-1] == "**" && g.Match(probe) {
			return false
		}
	}
	return true
}

// ParseSpec decodes a JSON policy spec.
func ParseSpec(b []byte) (Spec, error) {
	var s Spec
	if err := json.Unmarshal(b, &s); err != nil {
		return Spec{}, fmt.Errorf("policy: %v", err)
	}
	return s, nil
}
