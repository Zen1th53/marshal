package tui

// Verify screens: what each frozen Verify node renders.
//
// The rule this section enforces above all others is that a non-result never
// renders as a pass. A verification that has not run, a claim with no evidence,
// a required check with no outcome — each says exactly that, because the whole
// value of the section is that its "PASS" means something.

import "fmt"

// verifyScreens maps a frozen Verify spec id to its renderer.
func verifyScreens() map[string]func(VerifySnapshot) ScreenContent {
	screens := map[string]func(VerifySnapshot) ScreenContent{

		"CTUI-0336": func(v VerifySnapshot) ScreenContent { // Verify
			return ScreenContent{ReadOnly: true, Fields: []Field{
				{Label: "Verdict", Value: v.Verdict},
				{Label: "Session", Value: v.SessionID},
				{Label: "State", Value: v.State},
				{Label: "Required checks", Value: v.ChecksStatus},
				{Label: "Claims", Value: v.ClaimsStatus},
				{Label: "Evidence", Value: v.EvidenceStatus},
				{Label: "Attestation", Value: v.AttestationStatus},
			}}
		},

		"CTUI-0337": func(v VerifySnapshot) ScreenContent { // Verification session
			return ScreenContent{ReadOnly: true, Fields: []Field{
				{Label: "Session", Value: v.SessionID},
				{Label: "Version", Value: v.Version},
				{Label: "State", Value: v.State},
				{Label: "Risk tier", Value: v.RiskTier},
				// The binding is what makes the verification about something
				// specific rather than about the project in general.
				{Label: "Bound run", Value: v.BoundRun},
				{Label: "Bound plan", Value: v.BoundPlan},
				{Label: "Bound goal", Value: v.BoundGoal},
			}}
		},

		"CTUI-0345": func(v VerifySnapshot) ScreenContent { return claimLedger(v) },
		"CTUI-0357": func(v VerifySnapshot) ScreenContent { return evidenceLedger(v) },

		"CTUI-0377": func(v VerifySnapshot) ScreenContent { // findings
			content := ScreenContent{ReadOnly: true}
			if len(v.Contradictions) == 0 {
				content.Fields = []Field{
					{Label: "Contradictions", Value: Empty(verifySource)},
					{Label: "Verdict", Value: v.Verdict},
				}
				return content
			}
			for i, contradiction := range v.Contradictions {
				content.Fields = append(content.Fields, Field{
					Label: fmt.Sprintf("Contradiction %d", i+1), Value: contradiction})
			}
			return content
		},

		"CTUI-0391": func(v VerifySnapshot) ScreenContent { // completion
			content := ScreenContent{ReadOnly: true, Fields: []Field{
				{Label: "Verdict", Value: v.Verdict},
				{Label: "Attestation", Value: v.Attestation},
				{Label: "Required checks", Value: v.ChecksStatus},
			}}
			content.Fields = append(content.Fields, v.RequiredChecks...)
			// The limits are part of the answer: a verification that checked
			// three of ten things has not verified the other seven, and
			// omitting that would make a partial result look complete.
			content.Fields = append(content.Fields, limitFields(v)...)
			return content
		},
	}
	bindVerifyScreens(screens, reviewScreen, "CTUI-0339", "CTUI-0340", "CTUI-0341", "CTUI-0343", "CTUI-0344")
	bindVerifyScreens(screens, claimLedger, "CTUI-0346", "CTUI-0347", "CTUI-0348", "CTUI-0349", "CTUI-0350", "CTUI-0351", "CTUI-0353")
	bindVerifyScreens(screens, evidenceLedger, "CTUI-0354", "CTUI-0355", "CTUI-0356", "CTUI-0357", "CTUI-0358", "CTUI-0359", "CTUI-0360", "CTUI-0361", "CTUI-0362", "CTUI-0363")
	bindVerifyScreens(screens, methodsScreen, "CTUI-0364", "CTUI-0366", "CTUI-0367", "CTUI-0368", "CTUI-0369", "CTUI-0370", "CTUI-0371", "CTUI-0372", "CTUI-0373", "CTUI-0374", "CTUI-0375", "CTUI-0376")
	bindVerifyScreens(screens, findingsScreen, "CTUI-0378", "CTUI-0379", "CTUI-0380", "CTUI-0381")
	bindVerifyScreens(screens, alignmentScreen, "CTUI-0384", "CTUI-0385", "CTUI-0386", "CTUI-0387", "CTUI-0388", "CTUI-0389", "CTUI-0390")
	bindVerifyScreens(screens, completionScreen, "CTUI-0392", "CTUI-0393", "CTUI-0394", "CTUI-0396", "CTUI-0397")
	bindVerifyScreens(screens, bundleScreen, "CTUI-0398", "CTUI-0400", "CTUI-0401")
	bindVerifyScreens(screens, provenanceScreen, "CTUI-0403", "CTUI-0404", "CTUI-0405", "CTUI-0406", "CTUI-0407", "CTUI-0408", "CTUI-0409")
	bindVerifyValue(screens, func(v VerifySnapshot) Value { return v.State }, "CTUI-0344", "CTUI-0347", "CTUI-0353", "CTUI-0359")
	bindVerifyValue(screens, func(v VerifySnapshot) Value { return v.ClaimsStatus }, "CTUI-0348")
	bindVerifyValue(screens, func(v VerifySnapshot) Value { return v.EvidenceStatus }, "CTUI-0358", "CTUI-0361", "CTUI-0362", "CTUI-0400", "CTUI-0401", "CTUI-0404", "CTUI-0407", "CTUI-0408", "CTUI-0409")
	bindVerifyValue(screens, func(v VerifySnapshot) Value { return v.ChecksStatus }, "CTUI-0366", "CTUI-0367", "CTUI-0368", "CTUI-0369", "CTUI-0370", "CTUI-0371", "CTUI-0372", "CTUI-0373", "CTUI-0374", "CTUI-0375", "CTUI-0376")
	bindVerifyValue(screens, func(v VerifySnapshot) Value { return v.Verdict }, "CTUI-0380", "CTUI-0381", "CTUI-0385", "CTUI-0386", "CTUI-0387", "CTUI-0388", "CTUI-0389", "CTUI-0390", "CTUI-0392", "CTUI-0397")
	bindVerifyValue(screens, func(v VerifySnapshot) Value { return v.AttestationStatus }, "CTUI-0406")
	return screens
}

func bindVerifyValue(screens map[string]func(VerifySnapshot) ScreenContent, value func(VerifySnapshot) Value, ids ...string) {
	for _, specID := range ids {
		id := specID
		screens[id] = func(v VerifySnapshot) ScreenContent {
			return ScreenContent{ReadOnly: true, Fields: []Field{{Label: frozenTitle(id), Value: value(v)}}}
		}
	}
}

func bindVerifyScreens(screens map[string]func(VerifySnapshot) ScreenContent, render func(VerifySnapshot) ScreenContent, ids ...string) {
	for _, id := range ids {
		screens[id] = render
	}
}

func reviewScreen(v VerifySnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Status", v.State}, {"Verdict", v.Verdict}, {"Run", v.BoundRun}, {"Plan", v.BoundPlan}, {"Goal", v.BoundGoal}, {"Criteria", v.CriteriaStatus}, {"Claims", v.ClaimsStatus}, {"Evidence", v.EvidenceStatus}}}
}

func methodsScreen(v VerifySnapshot) ScreenContent {
	content := ScreenContent{ReadOnly: true, Fields: []Field{{"Required methods/checks", v.ChecksStatus}, {"Verdict", v.Verdict}}}
	content.Fields = append(content.Fields, v.RequiredChecks...)
	content.Fields = append(content.Fields, limitFields(v)...)
	return content
}

func findingsScreen(v VerifySnapshot) ScreenContent {
	content := ScreenContent{ReadOnly: true, Fields: []Field{{"Verdict", v.Verdict}}}
	for i, contradiction := range v.Contradictions {
		content.Fields = append(content.Fields, Field{fmt.Sprintf("Contradiction %d", i+1), contradiction})
	}
	content.Fields = append(content.Fields, limitFields(v)...)
	return content
}

func alignmentScreen(v VerifySnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Exact run", v.BoundRun}, {"Exact plan", v.BoundPlan}, {"Exact goal", v.BoundGoal}, {"Verification verdict", v.Verdict}, {"Evidence", v.EvidenceStatus}}}
}

func completionScreen(v VerifySnapshot) ScreenContent {
	content := ScreenContent{ReadOnly: true, Fields: []Field{{"Verdict", v.Verdict}, {"Mandatory checks", v.ChecksStatus}, {"Completion attestation", v.AttestationStatus}}}
	content.Fields = append(content.Fields, v.RequiredChecks...)
	content.Fields = append(content.Fields, limitFields(v)...)
	return content
}

func bundleScreen(v VerifySnapshot) ScreenContent { return evidenceLedger(v) }
func provenanceScreen(v VerifySnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Verification session", v.SessionID}, {"Run", v.BoundRun}, {"Plan", v.BoundPlan}, {"Goal", v.BoundGoal}, {"Evidence", v.EvidenceStatus}, {"Attestation", v.AttestationStatus}}}
}

func claimLedger(v VerifySnapshot) ScreenContent {
	content := ScreenContent{ReadOnly: true}
	if len(v.Claims) == 0 {
		content.Fields = []Field{{Label: "Claims", Value: v.ClaimsStatus}}
		return content
	}
	content.Fields = append(content.Fields, Field{Label: "Claims", Value: v.ClaimsStatus})
	for _, claim := range v.Claims {
		content.Fields = append(content.Fields,
			Field{Label: claim.ID.Display(), Value: claim.Status},
			Field{Label: "  scope", Value: claim.Text},
			Field{Label: "  evidence", Value: claim.Evidence})
	}
	return content
}

func evidenceLedger(v VerifySnapshot) ScreenContent {
	content := ScreenContent{ReadOnly: true}
	if len(v.Evidence) == 0 {
		content.Fields = []Field{{Label: "Evidence", Value: v.EvidenceStatus}}
		return content
	}
	content.Fields = append(content.Fields, Field{Label: "Evidence", Value: v.EvidenceStatus})
	for _, item := range v.Evidence {
		content.Fields = append(content.Fields,
			Field{Label: item.ID.Display(), Value: item.Status},
			Field{Label: "  kind", Value: item.Kind},
			// The content digest is what a later reader would check the
			// evidence against, so it is shown rather than summarised away.
			Field{Label: "  digest", Value: item.Digest})
	}
	return content
}

func limitFields(v VerifySnapshot) []Field {
	var fields []Field
	if len(v.Limitations) == 0 && len(v.KnownBlockers) == 0 {
		return append(fields, Field{Label: "Limitations", Value: Empty(verifySource)})
	}
	for i, limitation := range v.Limitations {
		fields = append(fields, Field{
			Label: fmt.Sprintf("Limitation %d", i+1), Value: limitation})
	}
	for i, blocker := range v.KnownBlockers {
		fields = append(fields, Field{
			Label: fmt.Sprintf("Known blocker %d", i+1), Value: blocker})
	}
	return fields
}

// RenderVerifyNode returns what a Verify node displays, if this build binds it.
func RenderVerifyNode(n *Node, v VerifySnapshot) (ScreenContent, bool) {
	if n == nil {
		return ScreenContent{}, false
	}
	render, ok := verifyScreens()[n.SpecID]
	if !ok {
		return ScreenContent{}, false
	}
	content := render(v)
	content.ReadOnly = isReadOnly(n)
	content = dedicatedLeafContent(n, content, verifySource)
	return content, true
}
