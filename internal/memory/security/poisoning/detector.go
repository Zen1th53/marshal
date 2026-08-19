package poisoning

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrCollusionDetected = errors.New("independent verification requirement violated: reviewer colluding with candidate author")
)

type ReasonCode string

const (
	ReasonProtectedTopicHijack ReasonCode = "PROTECTED_TOPIC_HIJACK"
	ReasonForgedEvidence       ReasonCode = "FORGED_EVIDENCE_ID"
	ReasonMassBurstRateLimit   ReasonCode = "MASS_BURST_RATE_LIMIT"
)

type InspectionResult struct {
	IsPoisoningSuspicious bool       `json:"is_poisoning_suspicious"`
	ShouldQuarantine      bool       `json:"should_quarantine"`
	Reason                ReasonCode `json:"reason,omitempty"`
	RiskScore             float64    `json:"risk_score"`
}

type Config struct {
	MaxBurstRate int // max writes per minute per project
}

type Detector struct {
	config Config
	mu     sync.Mutex
	burst  map[string][]time.Time
}

var (
	protectedTopicRegex = regexp.MustCompile(`(?i)(security\s+architecture|signature\s+verification|bypass|disable\s+auth|root\s+access|policy\s+rule)`)
)

func NewDetector(config Config) *Detector {
	if config.MaxBurstRate <= 0 {
		config.MaxBurstRate = 20
	}
	return &Detector{
		config: config,
		burst:  make(map[string][]time.Time),
	}
}

// InspectCandidate scores poisoning risk for new memory candidate records.
func (d *Detector) InspectCandidate(ctx context.Context, rec model.MemoryRecordV2, evidenceExists func(id string) bool) (InspectionResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now().UTC()

	// 1. Check rate-limit burst
	projectKey := rec.ProjectID
	var recent []time.Time
	cutoff := now.Add(-1 * time.Minute)
	for _, t := range d.burst[projectKey] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	recent = append(recent, now)
	d.burst[projectKey] = recent

	if len(recent) > d.config.MaxBurstRate {
		return InspectionResult{
			IsPoisoningSuspicious: true,
			ShouldQuarantine:      true,
			Reason:                ReasonMassBurstRateLimit,
			RiskScore:             0.95,
		}, nil
	}

	// 2. Protected topic hijack: Agent attempting to write security/policy architecture without operator authority
	isProtectedClaim := protectedTopicRegex.MatchString(rec.Title) || protectedTopicRegex.MatchString(rec.Body)
	if isProtectedClaim && rec.Authority != model.AuthorityOperator && rec.Authority != model.AuthorityPolicy {
		return InspectionResult{
			IsPoisoningSuspicious: true,
			ShouldQuarantine:      true,
			Reason:                ReasonProtectedTopicHijack,
			RiskScore:             0.90,
		}, nil
	}

	// 3. Forged evidence verification
	if evidenceExists != nil && len(rec.EvidenceIDs) > 0 {
		for _, eid := range rec.EvidenceIDs {
			if !evidenceExists(eid) {
				return InspectionResult{
					IsPoisoningSuspicious: true,
					ShouldQuarantine:      true,
					Reason:                ReasonForgedEvidence,
					RiskScore:             0.85,
				}, nil
			}
		}
	}

	return InspectionResult{
		IsPoisoningSuspicious: false,
		ShouldQuarantine:      false,
		RiskScore:             0.10,
	}, nil
}

// VerifyIndependentReview ensures the reviewer is not the same principal as the author.
func (d *Detector) VerifyIndependentReview(authorPrincipalID, reviewerPrincipalID string) error {
	if strings.TrimSpace(authorPrincipalID) == "" || strings.TrimSpace(reviewerPrincipalID) == "" {
		return ErrCollusionDetected
	}
	if strings.EqualFold(authorPrincipalID, reviewerPrincipalID) {
		return ErrCollusionDetected
	}
	return nil
}
