package tui

// Security screens: what each frozen Security node renders.
//
// Nothing here renders a secret. Grants, sandbox state and constitution version
// are shown in full because knowing who holds access is the point of the
// section; secret material is only ever counted.

func securityScreens() map[string]func(SecuritySnapshot) ScreenContent {
	screens := map[string]func(SecuritySnapshot) ScreenContent{

		"CTUI-0630": func(sec SecuritySnapshot) ScreenContent { // Security
			return ScreenContent{ReadOnly: true, Fields: []Field{
				{Label: "Constitution", Value: sec.Constitution},
				{Label: "Grants", Value: sec.GrantsStatus},
				{Label: "Sandbox", Value: sec.Sandbox},
				{Label: "Network", Value: sec.Network},
				{Label: "Secret leases", Value: sec.SecretLeases},
			}}
		},

		"CTUI-0631": func(sec SecuritySnapshot) ScreenContent { // Constitution
			return ScreenContent{ReadOnly: true, Fields: []Field{
				{Label: "Version", Value: sec.Constitution},
			}}
		},

		"CTUI-0658": func(sec SecuritySnapshot) ScreenContent { return grantsScreen(sec) },
		"CTUI-0666": func(sec SecuritySnapshot) ScreenContent { // Sandbox
			return ScreenContent{ReadOnly: true, Fields: []Field{
				{Label: "Isolation", Value: sec.Sandbox},
			}}
		},
		"CTUI-0673": func(sec SecuritySnapshot) ScreenContent { // Network
			return ScreenContent{ReadOnly: true, Fields: []Field{
				{Label: "Egress", Value: sec.Network},
			}}
		},
		"CTUI-0681": func(sec SecuritySnapshot) ScreenContent { // Secrets
			return ScreenContent{ReadOnly: true, Fields: []Field{
				// The count and nothing else. Secret material stays on the
				// execution host and never reaches this process.
				{Label: "Leases held", Value: sec.SecretLeases},
			}}
		},
	}
	bindSecurityScreens(screens, constitutionScreen, "CTUI-0632", "CTUI-0633", "CTUI-0634", "CTUI-0635", "CTUI-0636", "CTUI-0637", "CTUI-0638", "CTUI-0639", "CTUI-0640", "CTUI-0641")
	bindSecurityScreens(screens, policyScreen, "CTUI-0642", "CTUI-0643", "CTUI-0644", "CTUI-0645", "CTUI-0646", "CTUI-0647", "CTUI-0648")
	bindSecurityScreens(screens, riskScreen, "CTUI-0650", "CTUI-0651", "CTUI-0652", "CTUI-0653", "CTUI-0654", "CTUI-0655", "CTUI-0656", "CTUI-0657")
	bindSecurityScreens(screens, grantsScreen, "CTUI-0659", "CTUI-0660", "CTUI-0661", "CTUI-0662", "CTUI-0663")
	bindSecurityScreens(screens, sandboxScreen, "CTUI-0667", "CTUI-0668", "CTUI-0669", "CTUI-0670", "CTUI-0671", "CTUI-0672")
	bindSecurityScreens(screens, networkScreen, "CTUI-0674", "CTUI-0675", "CTUI-0676", "CTUI-0677", "CTUI-0678", "CTUI-0679", "CTUI-0680")
	bindSecurityScreens(screens, secretsScreen, "CTUI-0682", "CTUI-0683", "CTUI-0684", "CTUI-0685", "CTUI-0686", "CTUI-0687")
	bindSecurityScreens(screens, trustedScreen, "CTUI-0688", "CTUI-0689", "CTUI-0690", "CTUI-0691", "CTUI-0692", "CTUI-0693")
	bindSecurityScreens(screens, auditScreen, "CTUI-0694", "CTUI-0695", "CTUI-0696", "CTUI-0697", "CTUI-0698", "CTUI-0699", "CTUI-0700")
	bindSecurityValue(screens, func(s SecuritySnapshot) Value { return s.Constitution }, "CTUI-0633", "CTUI-0634", "CTUI-0635", "CTUI-0636", "CTUI-0637", "CTUI-0638", "CTUI-0639", "CTUI-0640", "CTUI-0641", "CTUI-0700")
	bindSecurityValue(screens, func(s SecuritySnapshot) Value { return s.Policy }, "CTUI-0645", "CTUI-0646", "CTUI-0647", "CTUI-0663", "CTUI-0675", "CTUI-0676", "CTUI-0680", "CTUI-0696")
	bindSecurityValue(screens, func(s SecuritySnapshot) Value { return s.GrantsStatus }, "CTUI-0659", "CTUI-0660", "CTUI-0695")
	bindSecurityValue(screens, func(s SecuritySnapshot) Value { return s.Sandbox }, "CTUI-0667", "CTUI-0669", "CTUI-0671", "CTUI-0672")
	bindSecurityValue(screens, func(s SecuritySnapshot) Value { return s.Network }, "CTUI-0678", "CTUI-0679")
	bindSecurityValue(screens, func(s SecuritySnapshot) Value { return s.SecretLeases }, "CTUI-0684", "CTUI-0685", "CTUI-0686")
	bindSecurityValue(screens, func(s SecuritySnapshot) Value { return s.TrustedContent }, "CTUI-0689", "CTUI-0691", "CTUI-0693")
	return screens
}

func bindSecurityValue(screens map[string]func(SecuritySnapshot) ScreenContent, value func(SecuritySnapshot) Value, ids ...string) {
	for _, specID := range ids {
		id := specID
		screens[id] = func(s SecuritySnapshot) ScreenContent {
			return ScreenContent{ReadOnly: true, Fields: []Field{{Label: frozenTitle(id), Value: value(s)}}}
		}
	}
}

func bindSecurityScreens(screens map[string]func(SecuritySnapshot) ScreenContent, render func(SecuritySnapshot) ScreenContent, ids ...string) {
	for _, id := range ids {
		screens[id] = render
	}
}

func constitutionScreen(sec SecuritySnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Constitution version", sec.Constitution}}}
}
func policyScreen(sec SecuritySnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Active policy", sec.Policy}, {"Constitution", sec.Constitution}}}
}
func riskScreen(sec SecuritySnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Risk/release gate", sec.RiskGates}, {"Policy", sec.Policy}}}
}
func sandboxScreen(sec SecuritySnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Isolation", sec.Sandbox}, {"Network enforcement", sec.Network}}}
}
func networkScreen(sec SecuritySnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Network enforcement", sec.Network}, {"Policy", sec.Policy}}}
}
func secretsScreen(sec SecuritySnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Secret leases (count only)", sec.SecretLeases}, {"Policy", sec.Policy}}}
}
func trustedScreen(sec SecuritySnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Trusted-content evidence", sec.TrustedContent}}}
}
func auditScreen(sec SecuritySnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Security audit", sec.Audit}, {"Grants", sec.GrantsStatus}, {"Sandbox", sec.Sandbox}, {"Network", sec.Network}}}
}

func grantsScreen(sec SecuritySnapshot) ScreenContent {
	content := ScreenContent{ReadOnly: true}
	if len(sec.Grants) == 0 {
		content.Fields = []Field{{Label: "Grants", Value: sec.GrantsStatus}}
		return content
	}
	content.Fields = append(content.Fields, Field{Label: "Grants", Value: sec.GrantsStatus})
	for _, grant := range sec.Grants {
		content.Fields = append(content.Fields,
			Field{Label: grant.ID.Display(), Value: grant.Standing},
			Field{Label: "  principal", Value: grant.Principal},
			Field{Label: "  role", Value: grant.Role},
			Field{Label: "  scope", Value: grant.Scope})
	}
	return content
}

// RenderSecurityNode returns what a Security node displays, if bound.
func RenderSecurityNode(n *Node, sec SecuritySnapshot) (ScreenContent, bool) {
	if n == nil {
		return ScreenContent{}, false
	}
	render, ok := securityScreens()[n.SpecID]
	if !ok {
		return ScreenContent{}, false
	}
	content := render(sec)
	content.ReadOnly = isReadOnly(n)
	content = dedicatedLeafContent(n, content, securityBinding)
	return content, true
}
