package tui

// System screens are explicit. A previous implementation filled the entire
// CTUI-0703..0803 numeric range with systemOverview, which made 90 unrelated
// leaf screens look implemented and corrupted acceptance coverage. Only the
// screens with a real, semantically matched renderer belong here.
func systemScreens() map[string]func(SystemSnapshot) ScreenContent {
	screens := map[string]func(SystemSnapshot) ScreenContent{
		"CTUI-0703": systemOverview,
		"CTUI-0704": runtimeStoreScreen,
		"CTUI-0713": resourcesScreen,
		"CTUI-0721": telemetryScreen,
		"CTUI-0734": lifecycleScreen,
		"CTUI-0740": mcpScreen,
		"CTUI-0749": a2aScreen,
		"CTUI-0760": tokensScreen,
		"CTUI-0768": backupScreen,
		"CTUI-0775": releaseScreen,
		"CTUI-0795": preferencesScreen,
	}
	bindSystemScreens(screens, runtimeStoreScreen, "CTUI-0706", "CTUI-0707", "CTUI-0708", "CTUI-0709", "CTUI-0710", "CTUI-0711", "CTUI-0712")
	bindSystemScreens(screens, resourcesScreen, "CTUI-0714", "CTUI-0715", "CTUI-0716", "CTUI-0717", "CTUI-0718", "CTUI-0719", "CTUI-0720")
	bindSystemScreens(screens, telemetryScreen, "CTUI-0722", "CTUI-0723", "CTUI-0724", "CTUI-0725", "CTUI-0726")
	bindSystemScreens(screens, apiScreen, "CTUI-0727", "CTUI-0728", "CTUI-0729", "CTUI-0730", "CTUI-0731", "CTUI-0732", "CTUI-0733")
	bindSystemScreens(screens, lifecycleScreen, "CTUI-0735", "CTUI-0736", "CTUI-0737", "CTUI-0738", "CTUI-0739")
	bindSystemScreens(screens, mcpScreen, "CTUI-0742", "CTUI-0743", "CTUI-0744", "CTUI-0745", "CTUI-0746", "CTUI-0747", "CTUI-0748")
	bindSystemScreens(screens, a2aScreen, "CTUI-0751", "CTUI-0752", "CTUI-0753", "CTUI-0754", "CTUI-0755", "CTUI-0756", "CTUI-0757", "CTUI-0758", "CTUI-0759")
	bindSystemScreens(screens, tokensScreen, "CTUI-0764", "CTUI-0765", "CTUI-0766")
	bindSystemScreens(screens, backupScreen, "CTUI-0770", "CTUI-0772", "CTUI-0773")
	bindSystemScreens(screens, releaseScreen, "CTUI-0776", "CTUI-0777", "CTUI-0778", "CTUI-0779", "CTUI-0780", "CTUI-0784", "CTUI-0786")
	bindSystemScreens(screens, conformanceScreen, "CTUI-0788", "CTUI-0789", "CTUI-0790", "CTUI-0791", "CTUI-0792", "CTUI-0793")
	bindSystemScreens(screens, preferencesScreen, "CTUI-0796", "CTUI-0797", "CTUI-0798", "CTUI-0799", "CTUI-0800", "CTUI-0801", "CTUI-0802")
	bindSystemValue(screens, func(s SystemSnapshot) Value { return s.Runtime }, "CTUI-0707", "CTUI-0712", "CTUI-0728", "CTUI-0735")
	bindSystemValue(screens, func(s SystemSnapshot) Value { return s.Agents }, "CTUI-0711")
	bindSystemValue(screens, func(s SystemSnapshot) Value { return s.Memory }, "CTUI-0715")
	bindSystemValue(screens, func(s SystemSnapshot) Value { return s.Storage }, "CTUI-0716")
	bindSystemValue(screens, func(s SystemSnapshot) Value { return s.ResourceHealth }, "CTUI-0720")
	bindSystemValue(screens, func(s SystemSnapshot) Value { return s.Telemetry }, "CTUI-0724")
	bindSystemValue(screens, func(s SystemSnapshot) Value { return s.API }, "CTUI-0732", "CTUI-0733")
	bindSystemValue(screens, func(s SystemSnapshot) Value { return s.Lifecycle }, "CTUI-0736", "CTUI-0737", "CTUI-0738")
	bindSystemValue(screens, func(s SystemSnapshot) Value { return s.MCP }, "CTUI-0742", "CTUI-0743", "CTUI-0744", "CTUI-0746", "CTUI-0747", "CTUI-0748", "CTUI-0779")
	bindSystemValue(screens, func(s SystemSnapshot) Value { return s.A2A }, "CTUI-0751", "CTUI-0752", "CTUI-0753", "CTUI-0754", "CTUI-0755", "CTUI-0756", "CTUI-0757", "CTUI-0758", "CTUI-0759")
	bindSystemValue(screens, func(s SystemSnapshot) Value { return s.TokensStatus }, "CTUI-0764")
	bindSystemValue(screens, func(s SystemSnapshot) Value { return s.Release }, "CTUI-0777", "CTUI-0784", "CTUI-0786", "CTUI-0789", "CTUI-0790", "CTUI-0792")
	bindSystemValue(screens, func(s SystemSnapshot) Value { return s.Preferences }, "CTUI-0796", "CTUI-0797", "CTUI-0798", "CTUI-0799", "CTUI-0800", "CTUI-0802")
	return screens
}

func bindSystemValue(screens map[string]func(SystemSnapshot) ScreenContent, value func(SystemSnapshot) Value, ids ...string) {
	for _, specID := range ids {
		id := specID
		screens[id] = func(s SystemSnapshot) ScreenContent {
			return ScreenContent{ReadOnly: true, Fields: []Field{{Label: frozenTitle(id), Value: value(s)}}}
		}
	}
}

func bindSystemScreens(screens map[string]func(SystemSnapshot) ScreenContent, render func(SystemSnapshot) ScreenContent, ids ...string) {
	for _, id := range ids {
		screens[id] = render
	}
}

func systemOverview(s SystemSnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{Label: "Runtime", Value: s.Runtime}, {Label: "Instance ID", Value: s.InstanceID}, {Label: "Schema", Value: s.SchemaVersion}, {Label: "Store integrity", Value: s.StoreIntegrity}, {Label: "Resources", Value: s.ResourceHealth}, {Label: "Tokens", Value: s.TokensStatus}, {Label: "Observed", Value: s.ObservedAt}}}
}
func runtimeStoreScreen(s SystemSnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Runtime", s.Runtime}, {"Instance ID", s.InstanceID}, {"Project", s.Project}, {"Schema", s.SchemaVersion}, {"Store integrity", s.StoreIntegrity}, {"Agents", s.Agents}, {"Sessions", s.Sessions}, {"Tasks", s.Tasks}, {"Leases", s.Leases}, {"Findings", s.Findings}, {"Approvals", s.Approvals}, {"Artifacts", s.Artifacts}, {"Events", s.Events}, {"API", s.API}}}
}
func resourcesScreen(s SystemSnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"CPU", s.CPU}, {"Memory", s.Memory}, {"Storage", s.Storage}, {"GPU", s.GPU}, {"Ollama", s.Ollama}, {"Health", s.ResourceHealth}}}
}
func mcpScreen(s SystemSnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"MCP", s.MCP}, {"Runtime", s.Runtime}, {"Tokens", s.TokensStatus}}}
}
func a2aScreen(s SystemSnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"A2A", s.A2A}, {"Runtime", s.Runtime}, {"Tokens", s.TokensStatus}}}
}
func telemetryScreen(s SystemSnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Telemetry", s.Telemetry}, {"Events", s.Events}}}
}
func apiScreen(s SystemSnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Local runtime API", s.API}, {"Runtime", s.Runtime}, {"Agents", s.Agents}, {"Tasks", s.Tasks}, {"Events", s.Events}, {"Artifacts", s.Artifacts}}}
}
func lifecycleScreen(s SystemSnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Lifecycle adapter", s.Lifecycle}, {"Runtime", s.Runtime}}}
}
func backupScreen(s SystemSnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Store integrity", s.StoreIntegrity}, {"Schema", s.SchemaVersion}, {"Artifacts", s.Artifacts}}}
}
func releaseScreen(s SystemSnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Release/conformance", s.Release}, {"Schema", s.SchemaVersion}, {"Runtime", s.Runtime}}}
}
func conformanceScreen(s SystemSnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Conformance/release result", s.Release}, {"Runtime", s.Runtime}, {"Schema", s.SchemaVersion}, {"Store integrity", s.StoreIntegrity}}}
}
func preferencesScreen(s SystemSnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Preferences", s.Preferences}, {"Observed", s.ObservedAt}}}
}

func tokensScreen(s SystemSnapshot) ScreenContent {
	content := ScreenContent{ReadOnly: true, Fields: []Field{{Label: "Tokens", Value: s.TokensStatus}}}
	for _, token := range s.Tokens {
		content.Fields = append(content.Fields, Field{Label: token.ID.Display(), Value: token.Standing}, Field{Label: "  name", Value: token.Name}, Field{Label: "  kind", Value: token.Kind}, Field{Label: "  capabilities", Value: token.Capabilities}, Field{Label: "  created", Value: token.Created}, Field{Label: "  credential", Value: token.Credential})
	}
	return content
}

// RenderSystemNode returns what a System node displays, if bound.
func RenderSystemNode(n *Node, s SystemSnapshot) (ScreenContent, bool) {
	if n == nil {
		return ScreenContent{}, false
	}
	render, ok := systemScreens()[n.SpecID]
	if !ok {
		return ScreenContent{}, false
	}
	content := render(s)
	content.ReadOnly = isReadOnly(n)
	content = dedicatedLeafContent(n, content, systemBinding)
	return content, true
}
