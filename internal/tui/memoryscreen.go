package tui

// Memory screens: what each frozen Memory node renders.
//
// The section shows a record's identity, standing and provenance freely, and
// its content only as a bounded excerpt that cannot be copied. That asymmetry
// is deliberate: knowing that MARSHAL remembers something, and where it learned
// it, is what a user needs; the raw text is what leaks.

func memoryScreens() map[string]func(MemorySnapshot) ScreenContent {
	screens := map[string]func(MemorySnapshot) ScreenContent{

		"CTUI-0410": func(m MemorySnapshot) ScreenContent { // Memory
			return ScreenContent{ReadOnly: true, Fields: []Field{
				{Label: "Service", Value: m.ServiceHealth},
				{Label: "Version", Value: m.ServiceVersion},
				{Label: "Project", Value: m.ServiceProject},
				{Label: "Records", Value: m.RecordsStatus},
			}}
		},

		"CTUI-0411": func(m MemorySnapshot) ScreenContent { return memoryLedger(m) },
		"CTUI-0440": func(m MemorySnapshot) ScreenContent { return memoryLedger(m) },
	}
	bindMemoryScreens(screens, memoryLedger, "CTUI-0412", "CTUI-0413", "CTUI-0414", "CTUI-0415", "CTUI-0416", "CTUI-0417", "CTUI-0418", "CTUI-0419", "CTUI-0420", "CTUI-0421", "CTUI-0422")
	bindMemoryScreens(screens, captureStatusScreen, "CTUI-0423")
	bindMemoryScreens(screens, workingMemoryScreen, "CTUI-0431", "CTUI-0432", "CTUI-0435")
	bindMemoryScreens(screens, memoryLedger, "CTUI-0441", "CTUI-0442", "CTUI-0443", "CTUI-0444", "CTUI-0445", "CTUI-0446", "CTUI-0447")
	bindMemoryScreens(screens, governanceMemoryScreen, "CTUI-0448", "CTUI-0449", "CTUI-0451", "CTUI-0452", "CTUI-0453", "CTUI-0454", "CTUI-0455", "CTUI-0459")
	bindMemoryScreens(screens, learningMemoryScreen, "CTUI-0460", "CTUI-0462", "CTUI-0463", "CTUI-0464", "CTUI-0466", "CTUI-0467", "CTUI-0468", "CTUI-0469", "CTUI-0470")
	bindMemoryScreens(screens, historyMemoryScreen, "CTUI-0471", "CTUI-0472", "CTUI-0473", "CTUI-0474", "CTUI-0475", "CTUI-0477")
	bindMemoryScreens(screens, importMemoryScreen, "CTUI-0479", "CTUI-0481", "CTUI-0482", "CTUI-0483", "CTUI-0488", "CTUI-0490")
	bindMemoryScreens(screens, integrityMemoryScreen, "CTUI-0492", "CTUI-0493", "CTUI-0494", "CTUI-0495", "CTUI-0496", "CTUI-0497", "CTUI-0498")
	bindMemoryValue(screens, func(m MemorySnapshot) Value { return m.RecordsStatus }, "CTUI-0412", "CTUI-0413", "CTUI-0415", "CTUI-0416", "CTUI-0417", "CTUI-0419", "CTUI-0420", "CTUI-0421", "CTUI-0422", "CTUI-0441", "CTUI-0442", "CTUI-0444", "CTUI-0445", "CTUI-0447", "CTUI-0449", "CTUI-0451", "CTUI-0452", "CTUI-0454", "CTUI-0455", "CTUI-0459", "CTUI-0464", "CTUI-0466", "CTUI-0467", "CTUI-0468", "CTUI-0470", "CTUI-0473", "CTUI-0474", "CTUI-0475", "CTUI-0477", "CTUI-0481", "CTUI-0482", "CTUI-0483", "CTUI-0488", "CTUI-0490", "CTUI-0497", "CTUI-0498")
	bindMemoryValue(screens, func(m MemorySnapshot) Value { return m.ServiceHealth }, "CTUI-0495", "CTUI-0496")
	bindMemoryLedgerExact(screens,
		"CTUI-0412", "CTUI-0413", "CTUI-0415", "CTUI-0416", "CTUI-0417", "CTUI-0419", "CTUI-0420", "CTUI-0421", "CTUI-0422",
		"CTUI-0441", "CTUI-0442", "CTUI-0444", "CTUI-0445", "CTUI-0447", "CTUI-0449", "CTUI-0451", "CTUI-0452", "CTUI-0454", "CTUI-0455", "CTUI-0459",
		"CTUI-0464", "CTUI-0466", "CTUI-0467", "CTUI-0468", "CTUI-0470", "CTUI-0473", "CTUI-0474", "CTUI-0475", "CTUI-0477",
		"CTUI-0481", "CTUI-0482", "CTUI-0483", "CTUI-0488", "CTUI-0490", "CTUI-0497", "CTUI-0498")
	return screens
}

func bindMemoryValue(screens map[string]func(MemorySnapshot) ScreenContent, value func(MemorySnapshot) Value, ids ...string) {
	for _, specID := range ids {
		id := specID
		screens[id] = func(m MemorySnapshot) ScreenContent {
			return ScreenContent{ReadOnly: true, Fields: []Field{{Label: frozenTitle(id), Value: value(m)}}}
		}
	}
}

func bindMemoryLedgerExact(screens map[string]func(MemorySnapshot) ScreenContent, ids ...string) {
	for _, specID := range ids {
		id := specID
		screens[id] = func(m MemorySnapshot) ScreenContent {
			content := memoryLedger(m)
			content.Fields = append([]Field{{Label: frozenTitle(id), Value: m.RecordsStatus}}, content.Fields...)
			return content
		}
	}
}

func bindMemoryScreens(screens map[string]func(MemorySnapshot) ScreenContent, render func(MemorySnapshot) ScreenContent, ids ...string) {
	for _, id := range ids {
		screens[id] = render
	}
}

func captureStatusScreen(m MemorySnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Memory service", m.ServiceHealth}, {"Canonical records", m.RecordsStatus}}}
}

func workingMemoryScreen(m MemorySnapshot) ScreenContent {
	content := ScreenContent{ReadOnly: true, Fields: []Field{{"Task-scoped slots", m.SlotsStatus}}}
	for _, slot := range m.Slots {
		content.Fields = append(content.Fields, Field{slot.Key.Display(), slot.Access}, Field{"  revision", slot.Revision})
	}
	return content
}

func governanceMemoryScreen(m MemorySnapshot) ScreenContent { return memoryLedger(m) }
func learningMemoryScreen(m MemorySnapshot) ScreenContent   { return memoryLedger(m) }
func historyMemoryScreen(m MemorySnapshot) ScreenContent    { return memoryLedger(m) }
func importMemoryScreen(m MemorySnapshot) ScreenContent     { return memoryLedger(m) }
func integrityMemoryScreen(m MemorySnapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Service", m.ServiceHealth}, {"Version", m.ServiceVersion}, {"Project", m.ServiceProject}, {"Canonical records", m.RecordsStatus}}}
}

func memoryLedger(m MemorySnapshot) ScreenContent {
	content := ScreenContent{ReadOnly: true}
	if len(m.Records) == 0 {
		content.Fields = []Field{{Label: "Records", Value: m.RecordsStatus}}
		return content
	}
	content.Fields = append(content.Fields, Field{Label: "Records", Value: m.RecordsStatus})
	for _, record := range m.Records {
		content.Fields = append(content.Fields,
			Field{Label: record.ID.Display(), Value: record.Standing},
			Field{Label: "  kind", Value: record.Kind},
			// Provenance is what makes a record checkable rather than merely
			// asserted, so it sits next to the standing it justifies.
			Field{Label: "  source", Value: record.Provenance},
			Field{Label: "  excerpt", Value: record.Excerpt},
			Field{Label: "  recorded", Value: record.When})
	}
	return content
}

// RenderMemoryNode returns what a Memory node displays, if this build binds it.
func RenderMemoryNode(n *Node, m MemorySnapshot) (ScreenContent, bool) {
	if n == nil {
		return ScreenContent{}, false
	}
	render, ok := memoryScreens()[n.SpecID]
	if !ok {
		return ScreenContent{}, false
	}
	content := render(m)
	content.ReadOnly = isReadOnly(n)
	content = dedicatedLeafContent(n, content, memoryBinding)
	return content, true
}
