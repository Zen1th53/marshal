// Package projectmemory assimilates what a project already knows about itself
// into MARSHAL's governed memory.
//
// A project arriving at MARSHAL often carries prior knowledge: a README, an
// architecture document, provider-native instruction files, a previous agent's
// notes, a copied .marshal directory. All of it is useful and none of it is
// trustworthy on arrival. The purpose of this package is to turn that material
// into candidates that Process 00's promotion gate can judge, rather than
// letting it become project truth by virtue of being present.
//
// The pipeline is:
//
//	discover → classify → redact → bind to project → assess freshness
//	→ record provenance → detect conflicts → assign trust
//	→ MemoryCandidate → (Process 00 gate decides)
//
// Nothing here promotes anything. The package's output is candidates; the
// constitutional gate in internal/constitution owns the decision, and an AI
// can neither shortcut that nor vouch for a source.
package projectmemory

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// SourceKind classifies where a piece of prior knowledge came from. The kind
// determines its default trust, because provenance is the only thing that
// distinguishes a verified fact from a confident sentence.
type SourceKind string

const (
	// SourceRepositoryFact is something observed directly from the repository:
	// which files exist, what the module is called, which languages are used.
	// It is checkable, and re-checkable.
	SourceRepositoryFact SourceKind = "repository-fact"
	// SourceGitHistory is derived from commit history. It is evidence about
	// what happened, though not about what should happen.
	SourceGitHistory SourceKind = "git-history"
	// SourceDocumentation is a README, architecture note or comment. It states
	// intentions that may never have been true, or may have stopped being true
	// years ago without anyone editing the file.
	SourceDocumentation SourceKind = "documentation"
	// SourceProviderInstruction is a native instruction file such as an agent
	// guidance document. It is governed input, never a directive.
	SourceProviderInstruction SourceKind = "provider-instruction"
	// SourceProviderMemory is memory a provider generated for itself. It is
	// the least trustworthy source: unverifiable, possibly stale, and the
	// natural vector for a poisoning attempt.
	SourceProviderMemory SourceKind = "provider-memory"
	// SourceImportedMarshalState is a .marshal directory that arrived with the
	// project. Trustworthy only if it belongs to this project.
	SourceImportedMarshalState SourceKind = "imported-marshal-state"
	// SourceAISummary is generated prose. A claim, not a finding.
	SourceAISummary SourceKind = "ai-summary"
)

// Trust is the default standing of a source before any evidence is applied.
type Trust string

const (
	// TrustVerifiable means the claim can be checked against the repository
	// deterministically, now and again later.
	TrustVerifiable Trust = "VERIFIABLE"
	// TrustEvidence means it is a record of something that happened.
	TrustEvidence Trust = "EVIDENCE"
	// TrustClaim means someone asserted it. It may be true.
	TrustClaim Trust = "CLAIM"
	// TrustUntrusted means it must not influence anything until validated.
	TrustUntrusted Trust = "UNTRUSTED"
)

// defaultTrust maps a source kind onto its standing.
//
// The table encodes the import trust matrix. Its shape is the point: only
// things MARSHAL can re-derive are verifiable, and anything a model produced
// about itself is untrusted regardless of how confident it reads.
var defaultTrust = map[SourceKind]Trust{
	SourceRepositoryFact:       TrustVerifiable,
	SourceGitHistory:           TrustEvidence,
	SourceDocumentation:        TrustClaim,
	SourceProviderInstruction:  TrustUntrusted,
	SourceProviderMemory:       TrustUntrusted,
	SourceImportedMarshalState: TrustUntrusted,
	SourceAISummary:            TrustClaim,
}

// TrustFor returns the default trust for a source kind. An unrecognised kind
// is untrusted, so adding a source without classifying it cannot accidentally
// grant it standing.
func TrustFor(kind SourceKind) Trust {
	if trust, ok := defaultTrust[kind]; ok {
		return trust
	}
	return TrustUntrusted
}

// Promotable reports whether a source's standing permits promotion at all
// without independent corroboration.
func (t Trust) Promotable() bool { return t == TrustVerifiable || t == TrustEvidence }

// Source is one discovered piece of prior knowledge.
type Source struct {
	Kind SourceKind `json:"kind"`
	// Path is where it was found, relative to the project root.
	Path string `json:"path,omitempty"`
	// Content is the raw material. It is redacted before anything else uses it.
	Content string `json:"-"`
	// ModifiedAt is when the source last changed, used for freshness.
	ModifiedAt time.Time `json:"modified_at,omitempty"`
	// ObservedCommit is the repository state the source was read at, so a
	// later check can tell whether the thing it describes has since changed.
	ObservedCommit string `json:"observed_commit,omitempty"`
}

// secretPatterns match credential-shaped material.
//
// This is a filter, not a guarantee. Its job is to stop the common shapes from
// being ingested at all; the structural protection is that redaction runs
// before any other stage, so nothing downstream ever sees the original text.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bAKIA[0-9A-Z]{16}\b`),             // AWS access key
	regexp.MustCompile(`(?i)\bgh[pousr]_[A-Za-z0-9]{20,}\b`),   // GitHub token
	regexp.MustCompile(`(?i)\bsk-[A-Za-z0-9]{20,}\b`),          // OpenAI-style key
	regexp.MustCompile(`(?i)\bxox[abprs]-[A-Za-z0-9-]{10,}\b`), // Slack token
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),   // private key block
	regexp.MustCompile(`(?i)\b(api[_-]?key|secret|token|password|passwd)\b\s*[:=]\s*\S+`),
	regexp.MustCompile(`(?i)\bey[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`), // JWT
}

// Redaction is the result of removing credential material.
type Redaction struct {
	// Text is the content with secrets replaced.
	Text string `json:"-"`
	// Found reports whether anything was redacted.
	Found bool `json:"found"`
	// Count is how many redactions were made, so a source that is mostly
	// credentials can be treated with more suspicion than one with a single
	// example key in a code sample.
	Count int `json:"count"`
}

// Redact removes credential-shaped material from text.
//
// It runs before classification, binding, or anything that persists, so a
// secret never reaches a later stage even if that stage would have rejected
// the source anyway. Redacting first costs nothing and removes an entire class
// of accident.
func Redact(content string) Redaction {
	redaction := Redaction{Text: content}
	for _, pattern := range secretPatterns {
		matches := pattern.FindAllString(redaction.Text, -1)
		if len(matches) == 0 {
			continue
		}
		redaction.Found = true
		redaction.Count += len(matches)
		redaction.Text = pattern.ReplaceAllString(redaction.Text, "[redacted]")
	}
	return redaction
}

// Freshness describes how current a source is relative to the project.
type Freshness string

const (
	// FreshCurrent means the source describes the current repository state.
	FreshCurrent Freshness = "FRESH"
	// FreshStale means the repository has changed since the source was written.
	FreshStale Freshness = "STALE"
	// FreshUnknown means it could not be established.
	FreshUnknown Freshness = "UNKNOWN"
)

// Current reports whether the source can be relied on as describing now.
// UNKNOWN does not qualify: not having established freshness is not evidence
// of it.
func (f Freshness) Current() bool { return f == FreshCurrent }

// AssessFreshness compares a source against the repository state.
//
// A source read at the current commit is fresh. One read at an older commit
// describes a repository that has since moved on, and is stale — which does
// not make it false, only unverified against the present.
func AssessFreshness(source Source, currentCommit string) Freshness {
	if strings.TrimSpace(source.ObservedCommit) == "" || strings.TrimSpace(currentCommit) == "" {
		return FreshUnknown
	}
	if source.ObservedCommit == currentCommit {
		return FreshCurrent
	}
	return FreshStale
}

// Candidate is a piece of prior knowledge prepared for the Process 00 gate.
type Candidate struct {
	// ID is stable for the same fact from the same source, so re-assimilating
	// a project does not create duplicates.
	ID string `json:"id"`
	// ProjectID binds the candidate to one project. Nothing may be promoted
	// without it, which is what makes cross-project leakage detectable.
	ProjectID projectid.ID `json:"project_id"`
	// Fact is the redacted statement.
	Fact string `json:"fact"`
	// Source describes where it came from.
	Kind SourceKind `json:"kind"`
	Path string     `json:"path,omitempty"`
	// Trust is the source's standing.
	Trust Trust `json:"trust"`
	// Freshness is its currency.
	Freshness Freshness `json:"freshness"`
	// SecretsRedacted reports that credential material was removed.
	SecretsRedacted bool `json:"secrets_redacted"`
	// ObservedCommit ties the candidate to a repository state.
	ObservedCommit string `json:"observed_commit,omitempty"`
	// ConflictsWith names candidates asserting something incompatible.
	ConflictsWith []string `json:"conflicts_with,omitempty"`
	// DuplicateOf names an earlier candidate stating the same thing.
	DuplicateOf string `json:"duplicate_of,omitempty"`
}

// candidateID derives a stable identifier from the project, source and fact.
func candidateID(project projectid.ID, kind SourceKind, path, fact string) string {
	h := sha256.New()
	h.Write([]byte("marshal.project.memory.candidate.v1\x00"))
	h.Write([]byte(project))
	h.Write([]byte{0})
	h.Write([]byte(kind))
	h.Write([]byte{0})
	h.Write([]byte(path))
	h.Write([]byte{0})
	h.Write([]byte(normalizeFact(fact)))
	return "MEMCAND-" + hex.EncodeToString(h.Sum(nil)[:12])
}

// normalizeFact reduces a statement to a comparable form so that trivially
// different phrasings of the same fact are recognised as duplicates.
func normalizeFact(fact string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(fact))), " ")
}

// AssimilationRequest carries everything needed to turn sources into candidates.
type AssimilationRequest struct {
	// ProjectID is the project the sources belong to. Assimilation refuses to
	// run without one, because an unbound candidate could be promoted into any
	// project.
	ProjectID projectid.ID
	// Sources are the discovered material.
	Sources []Source
	// CurrentCommit is the repository state at assimilation time.
	CurrentCommit string
}

// AssimilationResult is what the pipeline produced.
type AssimilationResult struct {
	Candidates []Candidate `json:"candidates"`
	// Rejected records sources that were dropped, with the reason, so a user
	// can see what was not ingested rather than only what was.
	Rejected []Rejection `json:"rejected,omitempty"`
	// SecretsFound reports how many sources carried credential material.
	SecretsFound int `json:"secrets_found"`
}

// Rejection records a source that did not become a candidate.
type Rejection struct {
	Kind   SourceKind `json:"kind"`
	Path   string     `json:"path,omitempty"`
	Reason string     `json:"reason"`
}

// Assimilate runs the pipeline over discovered sources.
//
// It produces candidates and nothing more. A candidate is a proposal; the
// Process 00 gate decides whether any of it becomes project memory, and this
// function deliberately has no way to shortcut that.
func Assimilate(request AssimilationRequest) AssimilationResult {
	var result AssimilationResult

	if !request.ProjectID.Valid() {
		// Without a project binding, every candidate would be promotable into
		// any project. Refusing the whole batch is the only safe answer.
		for _, source := range request.Sources {
			result.Rejected = append(result.Rejected, Rejection{
				Kind: source.Kind, Path: source.Path,
				Reason: "This project has no confirmed identity, so nothing can be attributed to it.",
			})
		}
		return result
	}

	seen := make(map[string]string)
	for _, source := range request.Sources {
		// Redaction runs first, so nothing downstream ever handles the
		// original text.
		redaction := Redact(source.Content)
		if redaction.Found {
			result.SecretsFound++
		}

		fact := strings.TrimSpace(redaction.Text)
		// A source whose entire content was credential material leaves nothing
		// but redaction markers. Keeping it would store a candidate that says
		// only that something was removed, which is not knowledge about the
		// project and would clutter memory with placeholders.
		if fact == "" || onlyRedactionMarkers(fact) {
			result.Rejected = append(result.Rejected, Rejection{
				Kind: source.Kind, Path: source.Path,
				Reason: "The source had no usable content after credential material was removed.",
			})
			continue
		}

		candidate := Candidate{
			ID:              candidateID(request.ProjectID, source.Kind, source.Path, fact),
			ProjectID:       request.ProjectID,
			Fact:            fact,
			Kind:            source.Kind,
			Path:            source.Path,
			Trust:           TrustFor(source.Kind),
			Freshness:       AssessFreshness(source, request.CurrentCommit),
			SecretsRedacted: redaction.Found,
			ObservedCommit:  source.ObservedCommit,
		}

		// Duplicates are linked rather than dropped, so the second occurrence
		// is still visible as corroboration from another source.
		fingerprint := normalizeFact(fact)
		if earlier, exists := seen[fingerprint]; exists {
			candidate.DuplicateOf = earlier
		} else {
			seen[fingerprint] = candidate.ID
		}

		result.Candidates = append(result.Candidates, candidate)
	}

	detectConflicts(result.Candidates)
	sort.SliceStable(result.Candidates, func(a, b int) bool {
		return result.Candidates[a].ID < result.Candidates[b].ID
	})
	return result
}

// negationMarkers are phrases that invert a statement's meaning.
var negationMarkers = []string{" not ", " never ", " no longer ", " isn't ", " does not ", " cannot "}

// detectConflicts links candidates that assert incompatible things about the
// same subject.
//
// The detection is deliberately shallow: it catches the case where two sources
// say the same thing with one negated. Anything subtler is left to the
// Control Intelligence review stage, which is where semantic judgement
// belongs. Detecting a conflict does not resolve it — both candidates stay,
// flagged, so the gate can refuse to promote either until the disagreement is
// settled.
func detectConflicts(candidates []Candidate) {
	for i := range candidates {
		for j := i + 1; j < len(candidates); j++ {
			if conflicting(candidates[i].Fact, candidates[j].Fact) {
				candidates[i].ConflictsWith = append(candidates[i].ConflictsWith, candidates[j].ID)
				candidates[j].ConflictsWith = append(candidates[j].ConflictsWith, candidates[i].ID)
			}
		}
	}
}

func conflicting(a, b string) bool {
	left, right := " "+normalizeFact(a)+" ", " "+normalizeFact(b)+" "
	leftNegated, rightNegated := hasNegation(left), hasNegation(right)
	if leftNegated == rightNegated {
		return false
	}
	// Strip the negation from whichever side carries it, then compare the
	// remaining content words. Comparing whole strings would miss the ordinary
	// case, because English changes the verb form around a negation: "uses"
	// becomes "does use". Dropping the auxiliaries that negation introduces
	// and comparing the words that carry meaning handles that without trying
	// to parse the sentence.
	stripped, other := stripNegation(left), right
	if rightNegated {
		stripped, other = stripNegation(right), left
	}
	return sameClaim(stripped, other)
}

// auxiliaries are the helper verbs that appear or change form around a
// negation. They carry no claim of their own, so removing them lets the
// negated and plain forms of a statement line up.
var auxiliaries = map[string]bool{
	"does": true, "do": true, "did": true, "is": true, "are": true,
	"was": true, "were": true, "be": true, "been": true, "has": true,
	"have": true, "had": true, "will": true, "would": true, "can": true,
	"could": true, "longer": true,
}

// contentWords reduces a statement to its meaning-bearing words, with simple
// verb endings normalized so "uses" and "use" compare equal.
func contentWords(text string) []string {
	var words []string
	for _, word := range strings.Fields(normalizeFact(text)) {
		if auxiliaries[word] {
			continue
		}
		// Trim a trailing plural or third-person "s" so that "uses" and "use"
		// are the same word. This is crude and deliberately so: it only needs
		// to be right often enough to surface a contradiction for review, and
		// a missed one falls through to the semantic review stage.
		if len(word) > 3 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") {
			word = strings.TrimSuffix(word, "s")
		}
		words = append(words, word)
	}
	return words
}

// sameClaim reports whether two statements assert the same thing once
// negation-related words are removed.
func sameClaim(a, b string) bool {
	left, right := contentWords(a), contentWords(b)
	if len(left) == 0 || len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func hasNegation(text string) bool {
	for _, marker := range negationMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func stripNegation(text string) string {
	for _, marker := range negationMarkers {
		if strings.Contains(text, marker) {
			return strings.Replace(text, marker, " ", 1)
		}
	}
	return text
}

// onlyRedactionMarkers reports whether nothing but redaction placeholders and
// punctuation survived.
func onlyRedactionMarkers(text string) bool {
	remaining := strings.TrimSpace(strings.ReplaceAll(strings.ToLower(text), "[redacted]", ""))
	return strings.Trim(remaining, " \t\n\r:=,.;-_\"'") == ""
}

// ToPromotionRequest converts a candidate into the form Process 00's gate
// evaluates.
//
// The mapping is where Process 02 hands authority over. Trust and freshness
// become inputs the gate weighs; they are not permissions. A candidate from an
// untrusted source arrives marked as AI-proposed with no evidence, which is
// precisely the shape the gate refuses.
func ToPromotionRequest(candidate Candidate, promotedBy string, evidenceIDs []string) constitution.PromotionRequest {
	memoryClass := constitution.ClassProject
	if candidate.Kind == SourceGitHistory || candidate.Kind == SourceRepositoryFact {
		memoryClass = constitution.ClassEvidence
	}

	// Only a source MARSHAL can re-derive counts as evidence-backed. Anything
	// else is proposed material that the gate must judge on its evidence, and
	// is marked as model-originated so the gate applies that standard.
	proposedByAI := !candidate.Trust.Promotable()

	// ContainsSecret is reported as false because the fact reaching the gate
	// has already been redacted — the credential is gone, not merely masked in
	// a copy. Passing true would reject text that no longer holds a secret.
	//
	// That a source *carried* credentials is still significant, so it is
	// handled where it belongs: such a source is treated as needing review
	// rather than promoted on its own terms.
	return constitution.PromotionRequest{
		Candidate: constitution.MemoryCandidate{
			ID:                      candidate.ID,
			ProjectID:               string(candidate.ProjectID),
			Class:                   memoryClass,
			Stage:                   constitution.StageValidated,
			Fact:                    candidate.Fact,
			ProposedBy:              string(candidate.Kind),
			ProposedByAI:            proposedByAI,
			EvidenceIDs:             evidenceIDs,
			EvidenceFresh:           candidate.Freshness.Current(),
			Contradicts:             candidate.ConflictsWith,
			ContainsSecret:          false,
			ContainsHiddenReasoning: candidate.Kind == SourceProviderMemory,
		},
		ProjectID:      string(candidate.ProjectID),
		PromotedBy:     promotedBy,
		ReviewConcerns: reviewConcerns(candidate),
	}
}

// reviewConcerns lists reasons a candidate should not be promoted unexamined.
// Any concern blocks promotion at the gate, so this is the mechanism by which
// a suspicious source is held back without needing a separate refusal path.
func reviewConcerns(candidate Candidate) []string {
	var concerns []string
	if candidate.SecretsRedacted {
		concerns = append(concerns, "the source contained credential material")
	}
	if candidate.Freshness == FreshStale {
		concerns = append(concerns, "the source describes an older state of the repository")
	}
	if candidate.Freshness == FreshUnknown {
		concerns = append(concerns, "the source could not be checked against the current repository")
	}
	if len(candidate.ConflictsWith) > 0 {
		concerns = append(concerns, "the source contradicts something else found in this project")
	}
	return concerns
}
