package reinjection

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/protocol"
)

// ConstraintCompiler compiles versioned, authoritative Goal constraints
// into bounded, trusted execution context for agents.
type ConstraintCompiler struct{}

func NewConstraintCompiler() *ConstraintCompiler {
	return &ConstraintCompiler{}
}

// Compile builds an immutable CompiledConstraints structure, filtering secret-bearing
// constraints if the recipient lacks authorized capabilities.
func (c *ConstraintCompiler) Compile(
	ctx context.Context,
	goal model.GoalContract,
	recipient protocol.Principal,
) (CompiledConstraints, error) {
	digest := ComputeConstraintsDigest(goal.Constraints, goal.DoNotDo)

	canAccessSecrets := hasSecretCapability(recipient)

	var hardXML, softXML strings.Builder
	for _, constraint := range goal.Constraints {
		stmt := constraint.Text
		if IsSecretConstraint(constraint) && !canAccessSecrets {
			stmt = "[REDACTED: capability required: secret:read]"
		}

		cBlock := fmt.Sprintf("    <constraint id=%q scope=%q source=%q is_hard=%q>\n      %s\n    </constraint>\n",
			constraint.ID, constraint.Scope, constraint.Source, fmt.Sprintf("%v", constraint.IsHard), stmt)

		if constraint.IsHard {
			hardXML.WriteString(cBlock)
		} else {
			softXML.WriteString(cBlock)
		}
	}

	var dndXML strings.Builder
	for _, rule := range goal.DoNotDo {
		dndXML.WriteString(fmt.Sprintf("    <rule>%s</rule>\n", strings.TrimSpace(rule)))
	}

	xmlBuilder := strings.Builder{}
	xmlBuilder.WriteString(fmt.Sprintf("<authoritative_constraints goal_id=%q revision=%q digest=%q>\n",
		goal.ID, fmt.Sprintf("%d", goal.Revision), digest))

	if hardXML.Len() > 0 {
		xmlBuilder.WriteString("  <hard_constraints>\n")
		xmlBuilder.WriteString(hardXML.String())
		xmlBuilder.WriteString("  </hard_constraints>\n")
	}

	if softXML.Len() > 0 {
		xmlBuilder.WriteString("  <soft_constraints>\n")
		xmlBuilder.WriteString(softXML.String())
		xmlBuilder.WriteString("  </soft_constraints>\n")
	}

	if dndXML.Len() > 0 {
		xmlBuilder.WriteString("  <do_not_do>\n")
		xmlBuilder.WriteString(dndXML.String())
		xmlBuilder.WriteString("  </do_not_do>\n")
	}

	xmlBuilder.WriteString("</authoritative_constraints>")

	return CompiledConstraints{
		GoalID:            goal.ID,
		Revision:          goal.Revision,
		Digest:            digest,
		ActiveConstraints: goal.Constraints,
		DoNotDo:           goal.DoNotDo,
		CompiledXML:       xmlBuilder.String(),
		CompiledAt:        time.Now().UTC(),
	}, nil
}

// InjectIntoPrompt prepends or embeds the authoritative constraints block at the start of a prompt.
func (c *ConstraintCompiler) InjectIntoPrompt(promptText string, compiled CompiledConstraints) string {
	cleanPrompt := strings.TrimSpace(promptText)
	if cleanPrompt == "" {
		return compiled.CompiledXML
	}

	// Remove any existing stale authoritative_constraints block before re-injecting
	cleanPrompt = stripExistingConstraintsBlock(cleanPrompt)

	return fmt.Sprintf("%s\n\n%s", compiled.CompiledXML, cleanPrompt)
}

// CompactWithConstraints ensures that during context compaction, the authoritative constraints
// block remains intact and authoritative at the top of the compacted summary.
func (c *ConstraintCompiler) CompactWithConstraints(
	priorPrompt string,
	compactedSummary string,
	compiled CompiledConstraints,
) string {
	cleanSummary := strings.TrimSpace(compactedSummary)
	cleanSummary = stripExistingConstraintsBlock(cleanSummary)

	return fmt.Sprintf("%s\n\n<compacted_context>\n%s\n</compacted_context>",
		compiled.CompiledXML, cleanSummary)
}

func stripExistingConstraintsBlock(text string) string {
	startTag := "<authoritative_constraints"
	endTag := "</authoritative_constraints>"

	startIdx := strings.Index(text, startTag)
	if startIdx == -1 {
		return text
	}

	endIdx := strings.Index(text, endTag)
	if endIdx == -1 {
		return text
	}

	prefix := text[:startIdx]
	suffix := text[endIdx+len(endTag):]

	return strings.TrimSpace(strings.TrimSpace(prefix) + " " + strings.TrimSpace(suffix))
}

func hasSecretCapability(p protocol.Principal) bool {
	if p.Role == protocol.RoleAppSec || p.Role == protocol.RoleOrchestrator {
		return true
	}
	for _, cap := range p.Capabilities {
		if cap == "secret:read" || cap == "secrets:access" || cap == "operator:admin" {
			return true
		}
	}
	return false
}
