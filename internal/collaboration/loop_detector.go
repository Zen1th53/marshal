package collaboration

import (
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

// LoopDetector identifies unproductive repetitive cycles across collaborative agent turns.
type LoopDetector struct {
	MaxRepeatedMessages int
	MaxStagnantTurns    int64
}

func NewLoopDetector() *LoopDetector {
	return &LoopDetector{
		MaxRepeatedMessages: 3,
		MaxStagnantTurns:    5,
	}
}

// Evaluate checks the recent communication and progress history for looping signatures.
func (d *LoopDetector) Evaluate(
	messages []model.AgentMessage,
	turnCount int64,
	hasNewEvidence bool,
	hasNewDiff bool,
) model.LoopDetectionResult {
	if len(messages) == 0 {
		return model.LoopDetectionResult{LoopDetected: false}
	}

	// 1. Repeated message / claim detection
	contentCounts := make(map[string]int)
	claimCounts := make(map[string]int)

	for _, m := range messages {
		normalizedContent := strings.TrimSpace(strings.ToLower(m.Content))
		if len(normalizedContent) > 10 {
			contentCounts[normalizedContent]++
			if contentCounts[normalizedContent] >= d.MaxRepeatedMessages {
				return model.LoopDetectionResult{
					LoopDetected:    true,
					LoopKind:        model.LoopRepeatedClaim,
					Reason:          fmt.Sprintf("Identical message repeated %d times without state progression", contentCounts[normalizedContent]),
					OffendingAgents: []string{m.From.AgentID},
				}
			}
		}

		for _, cid := range m.ClaimIDs {
			claimCounts[cid]++
			if claimCounts[cid] >= d.MaxRepeatedMessages && !hasNewEvidence {
				return model.LoopDetectionResult{
					LoopDetected:    true,
					LoopKind:        model.LoopRepeatedClaim,
					Reason:          fmt.Sprintf("Claim %s reiterated %d times without accompanying evidence", cid, claimCounts[cid]),
					OffendingAgents: []string{m.From.AgentID},
				}
			}
		}
	}

	// 2. Ping-pong detection (A -> B -> A -> B alternation with no new evidence or diff)
	if len(messages) >= 4 && !hasNewEvidence && !hasNewDiff {
		m1 := messages[len(messages)-1]
		m2 := messages[len(messages)-2]
		m3 := messages[len(messages)-3]
		m4 := messages[len(messages)-4]

		if m1.From.AgentID == m3.From.AgentID &&
			m2.From.AgentID == m4.From.AgentID &&
			m1.From.AgentID != m2.From.AgentID {
			return model.LoopDetectionResult{
				LoopDetected:    true,
				LoopKind:        model.LoopPingPong,
				Reason:          fmt.Sprintf("Ping-pong alternation between %s and %s with zero evidence or code progress", m1.From.AgentID, m2.From.AgentID),
				OffendingAgents: []string{m1.From.AgentID, m2.From.AgentID},
			}
		}
	}

	// 3. Bounced handoff detection
	if len(messages) >= 2 && !hasNewEvidence {
		mLast := messages[len(messages)-1]
		mPrev := messages[len(messages)-2]

		if (mLast.Kind == model.MessageHandoffProposal || mLast.Kind == model.MessageVerificationRequest) &&
			(mPrev.Kind == model.MessageHandoffProposal || mPrev.Kind == model.MessageVerificationRequest) &&
			mLast.From.AgentID == mPrev.To && mLast.To == mPrev.From.AgentID {
			return model.LoopDetectionResult{
				LoopDetected:    true,
				LoopKind:        model.LoopBouncedHandoff,
				Reason:          fmt.Sprintf("Handoff bounced back from %s to %s without new evidence", mLast.From.AgentID, mLast.To),
				OffendingAgents: []string{mLast.From.AgentID, mLast.To},
			}
		}
	}

	// 4. Stagnant turns with no progress
	if turnCount >= d.MaxStagnantTurns && !hasNewEvidence && !hasNewDiff {
		return model.LoopDetectionResult{
			LoopDetected: true,
			LoopKind:     model.LoopNoProgress,
			Reason:       fmt.Sprintf("Turn sequence reached %d with no new empirical evidence or code changes", turnCount),
		}
	}

	return model.LoopDetectionResult{LoopDetected: false}
}
