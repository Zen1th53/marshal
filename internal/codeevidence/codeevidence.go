// Package codeevidence adapts the vendored Code Evidence Context module
// (third_party/code-evidence) to MARSHAL.
//
// The adapter owns three decisions the module leaves to its host:
//
//   - Visibility. A task sees only its approved paths, minus the module's
//     default credential deny list and MARSHAL's own privacy rule
//     (projectknowledge.DeniedSourcePath). The policy is built here, on the
//     host, from the runtime's record of the task; no tool argument reaches it.
//   - Storage. The evidence store lives in the runtime state directory, outside
//     every task worktree and outside any agent's writable set.
//   - Freshness. Validation runs against an exact commit, materialized from
//     the object database with hardened, read-only git plumbing.
//
// The package has no edition tag. Only Extreme wiring imports it.
package codeevidence

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/code-evidence/evidence"
	"github.com/Zen1th53/marshal/internal/projectknowledge"
)

// Response bounds (bytes of model-facing text). They are below the module's
// own maxima so a tool result stays a small, predictable share of a turn.
const (
	DefaultContextBytes  = 6 << 10
	MaxContextBytes      = 8 << 10
	DefaultExpandBytes   = 4 << 10
	MaxExpandBytes       = 8 << 10
	DefaultValidateBytes = 2 << 10
	MaxValidateBytes     = 4 << 10
	// PushBytes bounds a pushed locator (setting code_evidence.push); the
	// cost model's locator is about 0.7-1.6 KB.
	PushBytes = 2 << 10
	// MaxRecords caps entries and expansion records.
	MaxRecords = 40
	// MaxReceiptsPerTask caps how many receipts one task attempt may record
	// and how many a validation checks.
	MaxReceiptsPerTask = 64
	// OperationTimeout bounds one provide, expand or validate call, including
	// the capture (index refresh) of the tree it reads.
	OperationTimeout = 60 * time.Second
)

// ErrNoScope is returned for a task without approved paths: code evidence is
// never served for an unscoped task.
var ErrNoScope = errors.New("code evidence: task has no approved source scope")

// ErrPrivacy is returned when a module result names a path that MARSHAL's
// privacy rule denies. The whole result is withheld (fail closed).
var ErrPrivacy = errors.New("code evidence: result withheld by the privacy rule")

// Host binds the adapter to one project.
type Host struct {
	// StateDir is the runtime state directory (.marshal-extreme). The
	// evidence store and temporary trees live below StateDir/code-evidence.
	StateDir string
	// Repository is the project checkout whose object database holds every
	// task commit (worktrees share it).
	Repository string
}

// Dir is the adapter's directory in the state directory.
func (h Host) Dir() string { return filepath.Join(h.StateDir, "code-evidence") }

// StoreDir is the host-owned evidence store.
func (h Host) StoreDir() string { return filepath.Join(h.Dir(), "store") }

// marshalDeny mirrors projectknowledge.DeniedSourcePath as policy globs, so
// denied files are dropped before any read (noninterference). The result
// check in checkPaths applies the exact rule again, including its
// case-insensitive parts, and withholds any result that names a denied path.
var marshalDeny = []string{
	"**/.git/**", "**/.marshal/**", "**/.marshal-extreme/**",
	"**/.codex/**", "**/.claude/**", "**/.opencode/**",
	"**/node_modules/**", "**/vendor/**",
	"**/credentials", "**/credentials/**", "**/secrets", "**/secrets/**",
	"**/transcripts/**", "**/sessions/**",
	".env", ".env.*", "*transcript*", "rollout-*", "id_rsa*", "id_ed25519*",
	"*.pem", "*.key", "*.jsonl", "*credential*",
	"internal/constitution/*protocol*",
}

// NormalizeScope validates approved task paths (files or directories,
// relative, clean) and returns them sorted without duplicates or trailing
// slashes. Any invalid entry fails the whole scope.
func NormalizeScope(approved []string) ([]string, error) {
	out := make([]string, 0, len(approved))
	for _, raw := range approved {
		p := strings.TrimSuffix(raw, "/")
		if p == "" || p == "." || path.IsAbs(p) || strings.ContainsAny(p, "\\:*?[]{}\x00\r\n") || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") {
			return nil, fmt.Errorf("code evidence: invalid approved path %q", raw)
		}
		out = append(out, p)
	}
	sort.Strings(out)
	out = slices.Compact(out)
	if len(out) == 0 {
		return nil, ErrNoScope
	}
	return out, nil
}

// Policy compiles the visibility policy for an approved scope: allow exactly
// the approved files and directory trees; deny the module's credential list
// and MARSHAL's privacy rule. Equal scopes give equal policy ids, so a
// receipt validates under the scope it was recorded with and no other.
func Policy(approved []string) (*evidence.Policy, error) {
	scope, err := NormalizeScope(approved)
	if err != nil {
		return nil, err
	}
	allow := make([]string, 0, 2*len(scope))
	for _, p := range scope {
		allow = append(allow, p, p+"/**")
	}
	base := evidence.DefaultPolicy().Spec().Deny
	deny := append(slices.Clone(base), marshalDeny...)
	return evidence.NewPolicy(evidence.PolicySpec{Name: "marshal-task-scope", Allow: allow, Deny: deny, PathRule: "marshal-projectknowledge-v1", DenyPath: projectknowledge.DeniedSourcePath}, nil)
}

func (h Host) open(ctx context.Context, root string, approved []string) (*evidence.Engine, error) {
	if h.StateDir == "" || root == "" {
		return nil, errors.New("code evidence: host is not configured")
	}
	pol, err := Policy(approved)
	if err != nil {
		return nil, err
	}
	return evidence.Open(ctx, evidence.Options{Root: root, StoreDir: h.StoreDir(), Policy: pol, Lexical: "go"})
}

func clamp(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

// checkPaths withholds a result that names a path MARSHAL's privacy rule
// denies. The policy globs already drop such paths; this is the exact rule.
func checkPaths(paths ...string) error {
	for _, p := range paths {
		if p != "" && projectknowledge.DeniedSourcePath(p) {
			return ErrPrivacy
		}
	}
	return nil
}

// Context builds a bounded dossier (pull mode) for task text over the live
// tree at root. recipe is "" (spans, predicates and map) or "locator";
// profile names the locator cost profile.
func (h Host) Context(ctx context.Context, root string, approved []string, task, recipe, profile string, budget int) (*evidence.Dossier, error) {
	if strings.TrimSpace(task) == "" {
		return nil, errors.New("code evidence: task text required")
	}
	if len(task) > 4096 {
		task = task[:4096]
	}
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	e, err := h.open(ctx, root, approved)
	if err != nil {
		return nil, err
	}
	defer e.Close()
	d, err := e.Provide(ctx, evidence.ProvideRequest{Task: task, Budget: clamp(budget, DefaultContextBytes, MaxContextBytes), MaxRecords: MaxRecords, Recipe: recipe, Profile: profile})
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(d.Entries)+len(d.FreeText))
	for _, en := range d.Entries {
		paths = append(paths, en.Path)
	}
	for _, p := range d.FreeText {
		paths = append(paths, p.Path)
	}
	for _, sp := range d.Receipt.Spans {
		paths = append(paths, sp.Path)
	}
	for _, p := range d.Receipt.Predicates {
		for _, it := range p.Items {
			paths = append(paths, it.Path)
		}
	}
	if err := checkPaths(paths...); err != nil {
		return nil, err
	}
	return d, nil
}

// Expand serves a bounded expansion of a receipt over the live tree at root.
func (h Host) Expand(ctx context.Context, root string, approved []string, req evidence.ExpandRequest) (*evidence.Expansion, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	e, err := h.open(ctx, root, approved)
	if err != nil {
		return nil, err
	}
	defer e.Close()
	req.Budget = clamp(req.Budget, DefaultExpandBytes, MaxExpandBytes)
	req.MaxRecords = clamp(req.MaxRecords, MaxRecords, MaxRecords)
	x, err := e.Expand(ctx, req)
	if err != nil {
		return nil, err
	}
	paths := append([]string(nil), x.Changed...)
	for _, r := range x.Records {
		paths = append(paths, r.Path)
	}
	if err := checkPaths(paths...); err != nil {
		return nil, err
	}
	return x, nil
}

// ValidateTree validates receipts against the tree at dir (a worktree or a
// materialized commit).
func (h Host) ValidateTree(ctx context.Context, dir string, approved, receipts []string) (*evidence.ValidationReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(receipts) == 0 {
		return &evidence.ValidationReport{Receipts: []evidence.ReceiptVerdict{}}, nil
	}
	if len(receipts) > MaxReceiptsPerTask {
		receipts = receipts[len(receipts)-MaxReceiptsPerTask:]
	}
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	e, err := h.open(ctx, dir, approved)
	if err != nil {
		return nil, err
	}
	defer e.Close()
	rep, err := e.Validate(ctx, evidence.ValidateRequest{Receipts: receipts})
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, r := range rep.Receipts {
		for _, s := range r.Spans {
			paths = append(paths, s.Path)
		}
		for _, p := range r.Predicates {
			for _, a := range p.Anchors {
				paths = append(paths, a.Path)
			}
		}
	}
	if err := checkPaths(paths...); err != nil {
		return nil, err
	}
	return rep, nil
}

// ValidateCommit materializes commit (only the paths visible under the
// approved scope) and validates receipts against it.
func (h Host) ValidateCommit(ctx context.Context, commit string, approved, receipts []string) (*evidence.ValidationReport, error) {
	if !hexID(commit) {
		return nil, errors.New("code evidence: invalid commit id")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(receipts) == 0 {
		return &evidence.ValidationReport{Receipts: []evidence.ReceiptVerdict{}}, nil
	}
	pol, err := Policy(approved)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	dir, cleanup, err := h.Materialize(ctx, commit, pol)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return h.ValidateTree(ctx, dir, approved, receipts)
}

// Summary condenses a validation report into the counts the inspector and
// review show. Spans and predicates are counted together:
//
//	holds       = spans unchanged + predicates holds
//	invalidated = spans changed or removed + predicates invalidated, on
//	              paths the task did not change itself (other work changed them)
//	own         = the same verdicts on paths the task changed itself (expected)
//	unknown     = spans or predicates unknown, plus receipts that could not
//	              be checked (not found, policy mismatch, revoked evidence)
type Summary struct {
	Receipts    int      `json:"receipts"`
	Holds       int      `json:"holds"`
	Invalidated int      `json:"invalidated"`
	Own         int      `json:"own"`
	Unknown     int      `json:"unknown"`
	Paths       []string `json:"paths,omitempty"` // invalidated evidence paths (not own), sorted, at most 8
}

// Summarize counts a report with no own changes.
func Summarize(rep *evidence.ValidationReport) Summary { return SummarizeAgainst(rep, nil) }

// SummarizeAgainst counts a report; changes on paths in own (the files the
// task itself touched) count as own, not invalidated.
func SummarizeAgainst(rep *evidence.ValidationReport, own []string) Summary {
	var s Summary
	if rep == nil {
		return s
	}
	mine := map[string]bool{}
	for _, p := range own {
		mine[p] = true
	}
	paths := map[string]bool{}
	for _, r := range rep.Receipts {
		s.Receipts++
		if r.Status != "checked" || (len(r.Spans) == 0 && len(r.Predicates) == 0) {
			s.Unknown++
			continue
		}
		for _, sp := range r.Spans {
			switch sp.Verdict {
			case evidence.SpanUnchanged:
				s.Holds++
			case evidence.SpanChanged, evidence.SpanRemoved:
				if mine[sp.Path] {
					s.Own++
					continue
				}
				s.Invalidated++
				paths[sp.Path] = true
			default:
				s.Unknown++
			}
		}
		for _, p := range r.Predicates {
			switch p.Verdict {
			case evidence.PredHolds:
				s.Holds++
			case evidence.PredInvalidated:
				foreign := len(p.Anchors) == 0
				for _, a := range p.Anchors {
					if !mine[a.Path] {
						foreign = true
						paths[a.Path] = true
					}
				}
				if foreign {
					s.Invalidated++
				} else {
					s.Own++
				}
			default:
				s.Unknown++
			}
		}
	}
	for p := range paths {
		if p != "" {
			s.Paths = append(s.Paths, p)
		}
	}
	sort.Strings(s.Paths)
	if len(s.Paths) > 8 {
		s.Paths = s.Paths[:8]
	}
	return s
}

// Text is the one-line form used in review notes and the inspector.
func (s Summary) Text() string {
	line := fmt.Sprintf("%d holds · %d invalidated · %d unknown (%d receipts", s.Holds, s.Invalidated, s.Unknown, s.Receipts)
	if s.Own > 0 {
		line += fmt.Sprintf(", %d changed by the task itself", s.Own)
	}
	line += ")"
	if len(s.Paths) > 0 {
		line += "; changed: " + strings.Join(s.Paths, ", ")
	}
	return line
}

// BoundedError is an error message cut to 240 bytes for audit records.
func BoundedError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 240 {
		msg = msg[:240]
	}
	return msg
}
