package collaboration

import "github.com/Zen1th53/marshal/internal/model"

type Participant = model.Participant
type TeamSession = model.TeamSession
type MessageKind = model.MessageKind
type AgentMessage = model.AgentMessage
type LoopKind = model.LoopKind
type LoopDetectionResult = model.LoopDetectionResult

const (
	MessageQuestion            = model.MessageQuestion
	MessageAnswer              = model.MessageAnswer
	MessageClaimChallenge      = model.MessageClaimChallenge
	MessageVerificationRequest = model.MessageVerificationRequest
	MessageHandoffProposal     = model.MessageHandoffProposal
	MessageFinding             = model.MessageFinding
	MessageFailedApproach      = model.MessageFailedApproach

	LoopRepeatedClaim  = model.LoopRepeatedClaim
	LoopPingPong       = model.LoopPingPong
	LoopNoProgress     = model.LoopNoProgress
	LoopBouncedHandoff = model.LoopBouncedHandoff
)
