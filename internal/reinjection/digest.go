package reinjection

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/protocol"
)

// ComputeConstraintsDigest calculates a deterministic canonical SHA-256 digest
// over all active constraints and do-not-do rules.
func ComputeConstraintsDigest(constraints []model.Constraint, doNotDo []string) string {
	// 1. Sort constraints by ID for deterministic ordering
	sorted := make([]model.Constraint, len(constraints))
	copy(sorted, constraints)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ID < sorted[j].ID
	})

	h := sha256.New()

	for _, c := range sorted {
		normID := strings.TrimSpace(c.ID)
		normScope := strings.ToLower(strings.TrimSpace(c.Scope))
		normStmt := strings.TrimSpace(c.Text)
		normSource := strings.TrimSpace(c.Source)
		isHardStr := "false"
		if c.IsHard {
			isHardStr = "true"
		}

		line := fmt.Sprintf("constraint:%s|%s|%s|%s|%s\n", normID, normScope, normStmt, normSource, isHardStr)
		h.Write([]byte(line))
	}

	// 2. Sort and hash do-not-do rules
	sortedDND := make([]string, len(doNotDo))
	copy(sortedDND, doNotDo)
	sort.Strings(sortedDND)

	for _, rule := range sortedDND {
		normRule := strings.TrimSpace(rule)
		line := fmt.Sprintf("do_not_do:%s\n", normRule)
		h.Write([]byte(line))
	}

	return fmt.Sprintf("sha256:%s", hex.EncodeToString(h.Sum(nil)))
}

// ComputeSingleConstraintDigest computes the deterministic digest for an individual constraint.
func ComputeSingleConstraintDigest(c model.Constraint) string {
	h := sha256.New()
	normID := strings.TrimSpace(c.ID)
	normScope := strings.ToLower(strings.TrimSpace(c.Scope))
	normStmt := strings.TrimSpace(c.Text)
	isHardStr := "false"
	if c.IsHard {
		isHardStr = "true"
	}
	line := fmt.Sprintf("%s|%s|%s|%s", normID, normScope, normStmt, isHardStr)
	h.Write([]byte(line))
	return fmt.Sprintf("sha256:%s", hex.EncodeToString(h.Sum(nil)))
}

// ExtractConstraintRefs builds the versioned protocol.ConstraintRef slice from a GoalContract.
func ExtractConstraintRefs(goal model.GoalContract) ([]protocol.ConstraintRef, string) {
	overallDigest := ComputeConstraintsDigest(goal.Constraints, goal.DoNotDo)

	refs := make([]protocol.ConstraintRef, 0, len(goal.Constraints))
	for _, c := range goal.Constraints {
		isSecret := IsSecretConstraint(c)
		cDigest := ComputeSingleConstraintDigest(c)

		refs = append(refs, protocol.ConstraintRef{
			ID:       c.ID,
			Revision: goal.Revision,
			Digest:   cDigest,
			Scope:    c.Scope,
			IsHard:   c.IsHard,
			IsSecret: isSecret,
		})
	}

	return refs, overallDigest
}

// IsSecretConstraint reports whether a constraint holds confidential or restricted material.
func IsSecretConstraint(c model.Constraint) bool {
	lowerScope := strings.ToLower(c.Scope)
	lowerStmt := strings.ToLower(c.Text)

	if strings.Contains(lowerScope, "secret") || strings.Contains(lowerScope, "credential") ||
		strings.Contains(lowerScope, "token") || strings.Contains(lowerScope, "private_key") {
		return true
	}

	if strings.Contains(lowerStmt, "api_key") || strings.Contains(lowerStmt, "secret_key") ||
		strings.Contains(lowerStmt, "confidential") || strings.Contains(lowerStmt, "bearer token") {
		return true
	}

	return false
}
