package app

import (
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/verification"
)

// verificationShortfallLimit bounds how much of one producer's reason a pause
// carries; the full record stays in the verification session.
const verificationShortfallLimit = 300

// verificationFailedPause is the resumable stop for a final check that ran and
// did not pass. It says in plain words which goal criteria were not accepted
// and why, as the producers recorded it; the bare decision code goes to Detail.
func verificationFailedPause(session verification.Session, result verification.Decision) marshal.Pause {
	reason := marshal.VerificationNotAccepted
	if lines := verificationShortfalls(session); len(lines) > 0 {
		reason += " " + strings.Join(lines, " ")
	}
	return marshal.Pause{Reason: reason, Resolutions: []string{marshal.VerificationNotAcceptedNext}, ResumeState: marshal.Verifying, Code: marshal.CodeVerificationFailed, Action: marshal.PauseResume, Detail: "verification failed: " + string(result)}
}

// verificationShortfalls lists each claim whose evidence did not pass, as
// "Not accepted: <criterion> — <reason>." in the session's claim order.
func verificationShortfalls(session verification.Session) []string {
	byID := make(map[string]verification.Evidence, len(session.Evidence))
	for _, ev := range session.Evidence {
		byID[ev.ID] = ev
	}
	var lines []string
	for _, claim := range session.Claims {
		var why []string
		for _, id := range claim.EvidenceIDs {
			ev, ok := byID[id]
			if !ok || ev.Status == verification.StatusPass {
				continue
			}
			why = append(why, shortfallWhy(ev))
		}
		if len(why) == 0 {
			continue
		}
		what := strings.TrimSpace(strings.Join(claim.SemanticScope, "; "))
		if what == "" {
			what = claim.CriterionID
		}
		lines = append(lines, "Not accepted: "+clipShortfall(what)+" — "+strings.Join(why, "; ")+".")
	}
	return lines
}

func shortfallWhy(ev verification.Evidence) string {
	if r := strings.Join(strings.Fields(ev.Reason), " "); r != "" {
		return clipShortfall(strings.TrimRight(r, "."))
	}
	if len(ev.Command) > 0 {
		return "the check `" + strings.Join(ev.Command, " ") + "` did not pass"
	}
	switch ev.Status {
	case verification.StatusUnknown:
		return "the independent check could not confirm it"
	case verification.StatusFail:
		return "the independent check found it not met"
	}
	return "its evidence is " + strings.ToLower(string(ev.Status))
}

func clipShortfall(s string) string {
	r := []rune(s)
	if len(r) <= verificationShortfallLimit {
		return s
	}
	return strings.TrimSpace(string(r[:verificationShortfallLimit-1])) + "…"
}
