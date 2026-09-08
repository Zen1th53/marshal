package verification

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type StabilityVerdict struct {
	Status              Status
	Attempts, Passes    int
	FailureFingerprints []string
	Quarantined         bool
}

func AssessStability(attempts []Status, fingerprints []string) StabilityVerdict {
	v := StabilityVerdict{Attempts: len(attempts), FailureFingerprints: append([]string(nil), fingerprints...)}
	for _, s := range attempts {
		if s == StatusPass {
			v.Passes++
		}
	}
	switch {
	case len(attempts) == 0:
		v.Status = StatusNotRun
	case v.Passes == len(attempts):
		v.Status = StatusPass
	case v.Passes == 0:
		v.Status = StatusFail
	default:
		v.Status = StatusUnknown
		v.Quarantined = true
	}
	return v
}

type OracleDescriptor struct{ ID, Provider, Implementation, Dataset, Author string }

func Independent(a, b OracleDescriptor) bool {
	return a.ID != "" && b.ID != "" && a.ID != b.ID && a.Provider != b.Provider && a.Implementation != b.Implementation && a.Dataset != b.Dataset && a.Author != b.Author
}

func Differential(left, right Status) Status {
	if !validStatus(left) || !validStatus(right) {
		return StatusUnknown
	}
	if left == right {
		return left
	}
	return StatusUnknown
}
func Metamorphic(base, transformed Status, relationHolds bool) Status {
	if base != StatusPass || transformed != StatusPass || !relationHolds {
		return StatusFail
	}
	return StatusPass
}

type ExternalEffectReceipt struct {
	Operation, Target, RequestedDigest, ObservedDigest, Observer string
	ObservedAt                                                   time.Time
}

func (r ExternalEffectReceipt) Verify() error {
	if r.Operation == "" || r.Target == "" || r.RequestedDigest == "" || r.ObservedDigest == "" || r.Observer == "" || r.ObservedAt.IsZero() {
		return fmt.Errorf("%w: external effect receipt", ErrInvalid)
	}
	if r.RequestedDigest != r.ObservedDigest {
		return ErrReplayDivergence
	}
	return nil
}

func ValidateWaiver(w Waiver, mandatory, critical bool, now time.Time) error {
	if mandatory || critical || w.Critical {
		return ErrUnsafeWaiver
	}
	if w.ID == "" || w.Actor == "" || strings.TrimSpace(w.Reason) == "" || !now.Before(w.ExpiresAt) {
		return fmt.Errorf("%w: waiver", ErrInvalid)
	}
	return nil
}

type ProviderAttempt struct {
	Provider, ConfigurationDigest string
	Status                        Status
	EvidenceIDs                   []string
}

func SelectProvider(attempts []ProviderAttempt, authorized map[string]string) (ProviderAttempt, error) {
	for _, a := range attempts {
		digest, ok := authorized[a.Provider]
		if !ok || digest == "" || digest != a.ConfigurationDigest {
			continue
		}
		if a.Status == StatusPass && len(a.EvidenceIDs) > 0 {
			return a, nil
		}
	}
	return ProviderAttempt{}, fmt.Errorf("%w: no governed verifier provider", ErrInvalid)
}

type Discovery struct {
	Source, Finding, EvidenceID string
	Status                      Status
}

func ValidateDiscoveries(items []Discovery) Status {
	for _, d := range items {
		if d.Source == "" || d.Finding == "" || d.EvidenceID == "" || d.Status != StatusPass {
			return StatusUnknown
		}
	}
	if len(items) == 0 {
		return StatusNotRun
	}
	return StatusPass
}

type Calibration struct{ TruePositive, FalsePositive, TrueNegative, FalseNegative int }

func (c Calibration) Rates() (precision, recall float64, status Status) {
	if c.TruePositive < 0 || c.FalsePositive < 0 || c.TrueNegative < 0 || c.FalseNegative < 0 {
		return 0, 0, StatusFail
	}
	pd := c.TruePositive + c.FalsePositive
	rd := c.TruePositive + c.FalseNegative
	if pd == 0 || rd == 0 {
		return 0, 0, StatusUnknown
	}
	return float64(c.TruePositive) / float64(pd), float64(c.TruePositive) / float64(rd), StatusPass
}

type SandboxPolicy struct {
	ReadOnlyTree    bool
	NetworkDisabled bool
	SecretsDisabled bool
	WritableRoots   []string
	InheritedFDs    []int
}

func (p SandboxPolicy) Validate() error {
	if !p.ReadOnlyTree || !p.NetworkDisabled || !p.SecretsDisabled || len(p.InheritedFDs) > 0 {
		return fmt.Errorf("%w: verification sandbox", ErrInvalid)
	}
	for _, root := range p.WritableRoots {
		if root == "" || root == "/" || root == "." || strings.Contains(root, "..") || !strings.HasPrefix(root, "/tmp/") {
			return fmt.Errorf("%w: writable verifier path", ErrInvalid)
		}
	}
	return nil
}

func CanonicalScopes(scopes []string) ([]string, error) {
	set := map[string]bool{}
	for _, scope := range scopes {
		s := strings.TrimSpace(scope)
		if s == "" || s == "*" || s == "all" || strings.Contains(s, "..") {
			return nil, fmt.Errorf("%w: semantic scope", ErrInvalid)
		}
		set[s] = true
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("%w: semantic scope", ErrInvalid)
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

func CalibrationWithin(c Calibration, maxFalseRate float64) bool {
	total := c.TruePositive + c.FalsePositive + c.TrueNegative + c.FalseNegative
	if total == 0 || math.IsNaN(maxFalseRate) || maxFalseRate < 0 {
		return false
	}
	return float64(c.FalsePositive+c.FalseNegative)/float64(total) <= maxFalseRate
}
