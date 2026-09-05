package collaboration_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/collaboration"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func createTestTeamSession(t *testing.T) (*store.Store, *collaboration.Coordinator, *model.TeamSession) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "collab_runtime_test.db")

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	coord := collaboration.NewCoordinator(st, nil)

	participants := []model.Participant{
		{
			AgentID:  "claude-arch",
			Role:     model.RoleArchitect,
			Harness:  "claude-code",
			Model:    "claude-3-7-sonnet",
			IsActive: true,
		},
		{
			AgentID:  "codex-dev",
			Role:     model.RoleDeveloper,
			Harness:  "codex",
			Model:    "gpt-4o",
			IsActive: true,
		},
		{
			AgentID:  "opencode-qa",
			Role:     model.RoleQA,
			Harness:  "opencode",
			Model:    "deepseek-coder",
			IsActive: true,
		},
		{
			AgentID:  "antigravity-int",
			Role:     model.RoleDeveloper,
			Harness:  "antigravity",
			Model:    "gemini-2.5-pro",
			IsActive: true,
		},
	}

	sess, err := coord.CreateSession(ctx, "sess-team-1", "goal-team-1", 1, participants)
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	return st, coord, sess
}

func TestFourAgentTeamSessionInitialization(t *testing.T) {
	st, _, sess := createTestTeamSession(t)
	defer st.Close()

	if len(sess.Participants) != 4 {
		t.Fatalf("expected 4 participants, got %d", len(sess.Participants))
	}
	if sess.ActiveTurn != "claude-arch" {
		t.Fatalf("expected initial active turn to be claude-arch, got %s", sess.ActiveTurn)
	}
	if sess.Status != "ACTIVE" {
		t.Fatalf("expected session status ACTIVE, got %s", sess.Status)
	}

	// Verify harness + model identity is separated from fixed role
	roleMap := make(map[model.Role][]string)
	for _, p := range sess.Participants {
		roleMap[p.Role] = append(roleMap[p.Role], p.Harness)
	}

	if len(roleMap[model.RoleArchitect]) != 1 || roleMap[model.RoleArchitect][0] != "claude-code" {
		t.Errorf("architect role binding mismatch: %v", roleMap[model.RoleArchitect])
	}
	if len(roleMap[model.RoleQA]) != 1 || roleMap[model.RoleQA][0] != "opencode" {
		t.Errorf("qa role binding mismatch: %v", roleMap[model.RoleQA])
	}
	if len(roleMap[model.RoleDeveloper]) != 2 {
		t.Errorf("expected 2 developers (codex and antigravity), got %d", len(roleMap[model.RoleDeveloper]))
	}
}

func TestTurnManagementAndHandoff(t *testing.T) {
	ctx := context.Background()
	st, coord, sess := createTestTeamSession(t)
	defer st.Close()

	now := time.Now().UTC()

	// 1. Claude posts architecture finding
	msgArch := model.AgentMessage{
		ID:        "msg-arch-1",
		SessionID: sess.SessionID,
		From: model.AuthorProvenance{
			AgentID:   "claude-arch",
			Harness:   "claude-code",
			Model:     "claude-3-7-sonnet",
			SessionID: sess.SessionID,
		},
		Kind:      model.MessageFinding,
		Content:   "Architecture defined: non-blocking token ring channel",
		CreatedAt: now,
	}
	if _, err := coord.SendMessage(ctx, msgArch, false, true); err != nil {
		t.Fatalf("send message arch failed: %v", err)
	}

	// 2. Claude hands off ownership to Codex (Developer)
	updatedSess, err := coord.HandOffOwnership(
		ctx,
		sess.SessionID,
		msgArch.From,
		model.RoleDeveloper,
		"Architecture spec finalized; please implement ring channel",
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("handoff to developer failed: %v", err)
	}
	if updatedSess.ActiveTurn != "codex-dev" {
		t.Fatalf("expected active turn to be codex-dev, got %s", updatedSess.ActiveTurn)
	}

	// 3. Codex implements and hands off to OpenCode (QA) with evidence
	codexProv := model.AuthorProvenance{
		AgentID:   "codex-dev",
		Harness:   "codex",
		Model:     "gpt-4o",
		SessionID: sess.SessionID,
	}
	qaSess, err := coord.HandOffOwnership(
		ctx,
		sess.SessionID,
		codexProv,
		model.RoleQA,
		"Channel implementation completed and local unit tests pass",
		[]string{"ev-unit-pass"},
		[]string{"claim-ring-correct"},
	)
	if err != nil {
		t.Fatalf("handoff to QA failed: %v", err)
	}
	if qaSess.ActiveTurn != "opencode-qa" {
		t.Fatalf("expected active turn to be opencode-qa, got %s", qaSess.ActiveTurn)
	}
}

func TestDisagreementCounterEvidenceChallenge(t *testing.T) {
	ctx := context.Background()
	st, coord, sess := createTestTeamSession(t)
	defer st.Close()

	now := time.Now().UTC()

	// 1. Save an initial supported claim from developer
	claim := model.Claim{
		ID:             "claim-latency-1",
		GoalID:         sess.GoalID,
		GoalRevision:   sess.GoalRevision,
		Subject:        "Channel latency under 5ms at 1000 qps",
		NormalizedText: "channel latency under 5ms at 1000 qps",
		Scope:          "internal/channel",
		Criticality:    model.CriticalityBlocker,
		State:          model.ClaimStateSupported,
		Author:         model.AuthorProvenance{AgentID: "codex-dev", Harness: "codex"},
		CreatedAt:      now,
	}
	if err := st.SaveClaim(ctx, claim); err != nil {
		t.Fatalf("save claim failed: %v", err)
	}

	// 2. QA runs benchmark and challenges claim with counter-evidence
	qaProv := model.AuthorProvenance{
		AgentID:   "opencode-qa",
		Harness:   "opencode",
		Model:     "deepseek-coder",
		SessionID: sess.SessionID,
	}
	counterEv := model.EvidenceRef{
		EvidenceID:      "ev-bench-fail",
		Digest:          "sha256:abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
		EvidenceType:    "benchmark_log",
		Tool:            "go_test",
		IsDeterministic: true,
	}

	err := coord.ChallengeClaim(ctx, sess.SessionID, qaProv, claim.ID, counterEv, "Empirical p99 latency observed at 14.8ms under 1000 qps load")
	if err != nil {
		t.Fatalf("challenge claim failed: %v", err)
	}

	// 3. Verify claim is now CONTESTED in store
	updatedClaim, err := st.GetClaim(ctx, claim.ID)
	if err != nil {
		t.Fatalf("get claim failed: %v", err)
	}
	if updatedClaim.State != model.ClaimStateContested {
		t.Fatalf("expected claim state CONTESTED, got %v", updatedClaim.State)
	}
	if len(updatedClaim.ContradictingEvidence) == 0 {
		t.Fatalf("expected contradicting evidence attached to claim")
	}

	// 4. Verify challenge message recorded in session
	msgs, err := st.ListAgentMessages(ctx, sess.SessionID, 10)
	if err != nil {
		t.Fatalf("list messages failed: %v", err)
	}
	foundChallengeMsg := false
	for _, m := range msgs {
		if m.Kind == model.MessageClaimChallenge && m.From.AgentID == "opencode-qa" {
			foundChallengeMsg = true
			break
		}
	}
	if !foundChallengeMsg {
		t.Fatalf("expected CLAIM_CHALLENGE message recorded in session")
	}
}

func TestLoopProtectionPingPong(t *testing.T) {
	ctx := context.Background()
	st, coord, sess := createTestTeamSession(t)
	defer st.Close()

	now := time.Now().UTC()

	provA := model.AuthorProvenance{AgentID: "codex-dev", Harness: "codex", SessionID: sess.SessionID}
	provB := model.AuthorProvenance{AgentID: "opencode-qa", Harness: "opencode", SessionID: sess.SessionID}

	// Message 1: A -> B
	m1 := model.AgentMessage{ID: "m1", SessionID: sess.SessionID, From: provA, To: "opencode-qa", Kind: model.MessageQuestion, Content: "Is this ready?", CreatedAt: now}
	_, _ = coord.SendMessage(ctx, m1, false, false)

	// Message 2: B -> A
	m2 := model.AgentMessage{ID: "m2", SessionID: sess.SessionID, From: provB, To: "codex-dev", Kind: model.MessageAnswer, Content: "Not yet", CreatedAt: now.Add(1 * time.Second)}
	_, _ = coord.SendMessage(ctx, m2, false, false)

	// Message 3: A -> B
	m3 := model.AgentMessage{ID: "m3", SessionID: sess.SessionID, From: provA, To: "opencode-qa", Kind: model.MessageQuestion, Content: "How about now?", CreatedAt: now.Add(2 * time.Second)}
	_, _ = coord.SendMessage(ctx, m3, false, false)

	// Message 4: B -> A (ping-pong threshold reached with zero evidence or diff)
	m4 := model.AgentMessage{ID: "m4", SessionID: sess.SessionID, From: provB, To: "codex-dev", Kind: model.MessageAnswer, Content: "Still not ready", CreatedAt: now.Add(3 * time.Second)}
	loopRes, err := coord.SendMessage(ctx, m4, false, false)

	if err == nil {
		t.Fatalf("expected loop detector to return error on ping-pong, got nil")
	}
	if loopRes == nil || !loopRes.LoopDetected || loopRes.LoopKind != model.LoopPingPong {
		t.Fatalf("expected LoopPingPong, got %+v", loopRes)
	}

	// Verify session status was automatically paused
	reSess, err := st.GetTeamSession(ctx, sess.SessionID)
	if err != nil {
		t.Fatalf("get session failed: %v", err)
	}
	if reSess.Status != "PAUSED" {
		t.Fatalf("expected session status PAUSED, got %s", reSess.Status)
	}
}

func TestLoopProtectionRepeatedClaim(t *testing.T) {
	ctx := context.Background()
	st, coord, sess := createTestTeamSession(t)
	defer st.Close()

	now := time.Now().UTC()
	provA := model.AuthorProvenance{AgentID: "codex-dev", Harness: "codex", SessionID: sess.SessionID}

	// Reiterate identical claim 3 times without new evidence
	for i := 1; i <= 2; i++ {
		msg := model.AgentMessage{
			ID:        time.Now().Format("20060102150405.000000000"),
			SessionID: sess.SessionID,
			From:      provA,
			Kind:      model.MessageFinding,
			Content:   "I am sure the benchmark passed already without issues",
			ClaimIDs:  []string{"claim-benchmark-fixed"},
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		}
		_, _ = coord.SendMessage(ctx, msg, false, false)
	}

	msg3 := model.AgentMessage{
		ID:        "m-repeat-3",
		SessionID: sess.SessionID,
		From:      provA,
		Kind:      model.MessageFinding,
		Content:   "I am sure the benchmark passed already without issues",
		ClaimIDs:  []string{"claim-benchmark-fixed"},
		CreatedAt: now.Add(3 * time.Second),
	}
	loopRes, err := coord.SendMessage(ctx, msg3, false, false)

	if err == nil {
		t.Fatalf("expected loop detector to catch repeated message, got nil")
	}
	if loopRes == nil || !loopRes.LoopDetected || loopRes.LoopKind != model.LoopRepeatedClaim {
		t.Fatalf("expected LoopRepeatedClaim, got %+v", loopRes)
	}
}

func TestDurableRestartMidCollaboration(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "collab_restart_test.db")

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	coord := collaboration.NewCoordinator(st, nil)

	participants := []model.Participant{
		{AgentID: "claude-arch", Role: model.RoleArchitect, Harness: "claude-code", IsActive: true},
		{AgentID: "codex-dev", Role: model.RoleDeveloper, Harness: "codex", IsActive: true},
		{AgentID: "antigravity-int", Role: model.RoleDeveloper, Harness: "antigravity", IsActive: true},
	}

	sess, err := coord.CreateSession(ctx, "sess-restart-1", "goal-restart-1", 1, participants)
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	// Post finding and transition turn
	_, _ = coord.StartTurn(ctx, sess.SessionID, "codex-dev")
	msg := model.AgentMessage{
		ID:        "msg-restart-1",
		SessionID: sess.SessionID,
		From:      model.AuthorProvenance{AgentID: "codex-dev", Harness: "codex"},
		Kind:      model.MessageFinding,
		Content:   "Discovered dependency conflict in sqlite driver version",
		CreatedAt: time.Now().UTC(),
	}
	_, _ = coord.SendMessage(ctx, msg, false, true)

	// Simulate crash / restart: close store, open fresh coordinator
	if err := st.Close(); err != nil {
		t.Fatalf("failed to close store: %v", err)
	}

	st2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen store failed: %v", err)
	}
	defer st2.Close()

	coord2 := collaboration.NewCoordinator(st2, nil)

	// Antigravity resumes session and discovers peer work from durable state
	overview, err := coord2.GetSessionOverview(ctx, sess.SessionID)
	if err != nil {
		t.Fatalf("GetSessionOverview after restart failed: %v", err)
	}

	if overview.Session.ActiveTurn != "codex-dev" {
		t.Fatalf("expected active turn codex-dev, got %s", overview.Session.ActiveTurn)
	}
	if len(overview.Session.Participants) != 3 {
		t.Fatalf("expected 3 participants, got %d", len(overview.Session.Participants))
	}
	if len(overview.RecentTurns) != 1 || overview.RecentTurns[0].Content != "Discovered dependency conflict in sqlite driver version" {
		t.Fatalf("unexpected recent turns discovered after restart: %+v", overview.RecentTurns)
	}
}
