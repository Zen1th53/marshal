package app

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/verification"
)

// A final check that ran and did not pass names each criterion it did not
// accept and the verifier's own reason, instead of the bare decision code
// (the v0.2.14 Ultron run stopped on "verification failed: VERIFICATION_FAILED"
// with the verifier's reasons nowhere to be read).
func TestFailedVerificationPauseSaysWhichCriteriaAndWhy(t *testing.T) {
	session := verification.Session{
		Claims: []verification.Claim{
			{ID: "ui", SemanticScope: []string{"Interface: blue orb, text chat"}, EvidenceIDs: []string{"rt-ui", "v-0"}},
			{ID: "voice", SemanticScope: []string{"Voice: understands Uzbek speech"}, EvidenceIDs: []string{"rt-voice", "v-1"}},
			{ID: "local", SemanticScope: []string{"Local first"}, EvidenceIDs: []string{"rt-local", "v-2"}},
			{ID: "tests", SemanticScope: []string{"Tests pass"}, EvidenceIDs: []string{"rt-tests"}},
		},
		Evidence: []verification.Evidence{
			{ID: "rt-ui", Status: verification.StatusPass, Command: []string{"pytest tests/ui"}},
			{ID: "v-0", Status: verification.StatusUnknown},
			{ID: "rt-voice", Status: verification.StatusPass},
			{ID: "v-1", Status: verification.StatusFail, Reason: "  Only an adapter protocol;\n no Uzbek speech model is bundled.  "},
			{ID: "rt-local", Status: verification.StatusPass},
			{ID: "v-2", Status: verification.StatusPass, Reason: "Works offline."},
			{ID: "rt-tests", Status: verification.StatusFail, Command: []string{"python", "-m", "pytest", "-q"}},
		},
	}
	p := verificationFailedPause(session, verification.VerificationFailed)
	want := "The final check did not accept the result." +
		" Not accepted: Interface: blue orb, text chat — the independent check could not confirm it." +
		" Not accepted: Voice: understands Uzbek speech — Only an adapter protocol; no Uzbek speech model is bundled." +
		" Not accepted: Tests pass — the check `python -m pytest -q` did not pass."
	if p.Reason != want {
		t.Fatalf("reason:\n got %q\nwant %q", p.Reason, want)
	}
	if strings.Contains(p.Reason, "VERIFICATION_FAILED") || p.Detail != "verification failed: VERIFICATION_FAILED" {
		t.Fatalf("code belongs in Detail only: %+v", p)
	}
	if p.Code != marshal.CodeVerificationFailed || p.Action != marshal.PauseResume || p.ResumeState != marshal.Verifying || len(p.Resolutions) != 1 || p.Resolutions[0] != marshal.VerificationNotAcceptedNext {
		t.Fatalf("pause must stay the same resumable stop: %+v", p)
	}
	long := strings.Repeat("x", 2000)
	session.Evidence[3].Reason = long
	if got := verificationFailedPause(session, verification.VerificationFailed).Reason; len([]rune(got)) > 1000 || !strings.Contains(got, "…") {
		t.Fatalf("a long reason is not bounded: %d", len(got))
	}
	if got := verificationFailedPause(verification.Session{}, verification.VerificationFailed).Reason; got != marshal.VerificationNotAccepted {
		t.Fatalf("empty session: %q", got)
	}
}
