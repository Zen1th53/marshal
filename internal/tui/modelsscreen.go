package tui

// Models screens: what each frozen Models node renders.
//
// Two of the seventeen frozen implementation gaps live in this section, and
// both concern things a provider does not tell anyone: a machine-readable
// detail surface (CTUI-0523) and entitlement request state (CTUI-0557). They
// render as gaps wherever they appear rather than as an empty table, because
// an empty table here reads as "nothing to report" rather than "not knowable".

func modelsScreens() map[string]func(ModelsSnapshot) ScreenContent {
	screens := map[string]func(ModelsSnapshot) ScreenContent{

		"CTUI-0501": func(m ModelsSnapshot) ScreenContent { // Models
			return ScreenContent{ReadOnly: true, Fields: []Field{
				{Label: "Local models", Value: m.LocalStatus},
				{Label: "Optimization cycles", Value: m.CyclesStatus},
				{Label: "Canaries", Value: m.CanaryStatus},
			}}
		},

		"CTUI-0523": func(m ModelsSnapshot) ScreenContent { // provider detail: a gap
			return ScreenContent{
				ReadOnly: true, HasNotice: true, Notice: m.ProviderDetail,
			}
		},

		"CTUI-0557": func(m ModelsSnapshot) ScreenContent { // entitlement: a gap
			return ScreenContent{
				ReadOnly: true, HasNotice: true, Notice: m.Entitlement,
			}
		},

		"CTUI-0566": func(m ModelsSnapshot) ScreenContent { return localModelsScreen(m) },
		"CTUI-0570": func(m ModelsSnapshot) ScreenContent { return optimizationScreen(m) },
	}
	bindModelValue(screens, func(m ModelsSnapshot) Value { return m.ProviderStatus }, "CTUI-0502", "CTUI-0503", "CTUI-0504", "CTUI-0505", "CTUI-0506", "CTUI-0507", "CTUI-0508", "CTUI-0509", "CTUI-0510", "CTUI-0511", "CTUI-0512", "CTUI-0513", "CTUI-0514", "CTUI-0515", "CTUI-0516", "CTUI-0517", "CTUI-0518", "CTUI-0519", "CTUI-0520", "CTUI-0521", "CTUI-0522")
	bindModelValue(screens, func(m ModelsSnapshot) Value { return m.LocalStatus }, "CTUI-0524", "CTUI-0525", "CTUI-0526", "CTUI-0527", "CTUI-0528", "CTUI-0529")
	bindModelValue(screens, func(m ModelsSnapshot) Value { return m.RoutingStatus }, "CTUI-0530", "CTUI-0531", "CTUI-0532", "CTUI-0533", "CTUI-0534", "CTUI-0535", "CTUI-0536", "CTUI-0538", "CTUI-0539")
	bindModelValue(screens, func(m ModelsSnapshot) Value { return m.ProviderStatus }, "CTUI-0540", "CTUI-0541", "CTUI-0542", "CTUI-0543", "CTUI-0544", "CTUI-0545", "CTUI-0546")
	bindModelValue(screens, func(m ModelsSnapshot) Value { return m.ContextStatus }, "CTUI-0547", "CTUI-0548", "CTUI-0549", "CTUI-0550", "CTUI-0551", "CTUI-0552", "CTUI-0553")
	bindModelValue(screens, func(m ModelsSnapshot) Value { return m.CloudStatus }, "CTUI-0554", "CTUI-0555", "CTUI-0556", "CTUI-0558", "CTUI-0559", "CTUI-0560", "CTUI-0561")
	bindModelValue(screens, func(m ModelsSnapshot) Value { return m.EvaluationStatus }, "CTUI-0564", "CTUI-0565", "CTUI-0566", "CTUI-0567", "CTUI-0568", "CTUI-0569")
	bindModelValue(screens, func(m ModelsSnapshot) Value { return m.CyclesStatus }, "CTUI-0571", "CTUI-0572", "CTUI-0574", "CTUI-0575", "CTUI-0576", "CTUI-0577", "CTUI-0578", "CTUI-0579", "CTUI-0580", "CTUI-0581", "CTUI-0582")
	bindModelValue(screens, func(m ModelsSnapshot) Value { return m.CounterfactualStatus }, "CTUI-0584", "CTUI-0585", "CTUI-0586", "CTUI-0588", "CTUI-0589", "CTUI-0590")
	bindModelValue(screens, func(m ModelsSnapshot) Value { return m.BenchmarkStatus }, "CTUI-0591", "CTUI-0592", "CTUI-0593", "CTUI-0594", "CTUI-0595", "CTUI-0596", "CTUI-0597", "CTUI-0598", "CTUI-0599", "CTUI-0600", "CTUI-0601", "CTUI-0602", "CTUI-0603", "CTUI-0604", "CTUI-0605", "CTUI-0606", "CTUI-0607")
	bindModelValue(screens, func(m ModelsSnapshot) Value { return m.ResultsStatus }, "CTUI-0608", "CTUI-0609", "CTUI-0610", "CTUI-0611", "CTUI-0612", "CTUI-0613", "CTUI-0614", "CTUI-0615")
	bindModelValue(screens, func(m ModelsSnapshot) Value { return m.CanaryStatus }, "CTUI-0616", "CTUI-0618", "CTUI-0619", "CTUI-0620", "CTUI-0622")
	bindModelValue(screens, func(m ModelsSnapshot) Value { return m.FeedbackStatus }, "CTUI-0625", "CTUI-0627", "CTUI-0628", "CTUI-0629")
	bindModelValue(screens, func(m ModelsSnapshot) Value { return m.EvaluationStatus }, "CTUI-0570")
	return screens
}

func bindModelValue(screens map[string]func(ModelsSnapshot) ScreenContent, value func(ModelsSnapshot) Value, ids ...string) {
	for _, specID := range ids {
		id := specID
		screens[id] = func(m ModelsSnapshot) ScreenContent {
			return modelFields(Field{frozenTitle(id), value(m)})
		}
	}
}

func bindModelScreens(screens map[string]func(ModelsSnapshot) ScreenContent, render func(ModelsSnapshot) ScreenContent, ids ...string) {
	for _, id := range ids {
		screens[id] = render
	}
}

func modelFields(fields ...Field) ScreenContent { return ScreenContent{ReadOnly: true, Fields: fields} }
func providerScreens(m ModelsSnapshot) ScreenContent {
	return modelFields(Field{"Provider/harness evidence", m.ProviderStatus})
}
func routingScreens(m ModelsSnapshot) ScreenContent {
	return modelFields(Field{"Canonical route", m.RoutingStatus}, Field{"Optimization evidence", m.CyclesStatus})
}
func harnessScreens(m ModelsSnapshot) ScreenContent {
	return modelFields(Field{"Harness capability/qualification evidence", m.ProviderStatus})
}
func contextScreens(m ModelsSnapshot) ScreenContent {
	return modelFields(Field{"Compiled context", m.ContextStatus})
}
func cloudScreens(m ModelsSnapshot) ScreenContent {
	return modelFields(Field{"Community Cloud / ULTRA", m.CloudStatus})
}
func evaluationScreens(m ModelsSnapshot) ScreenContent {
	return modelFields(Field{"Evaluation", m.EvaluationStatus}, Field{"Cycles", m.CyclesStatus})
}
func counterfactualScreens(m ModelsSnapshot) ScreenContent {
	return modelFields(Field{"Counterfactual", m.CounterfactualStatus}, Field{"Cycle", m.CyclesStatus})
}
func benchmarkScreens(m ModelsSnapshot) ScreenContent {
	return modelFields(Field{"Benchmarks", m.BenchmarkStatus}, Field{"Cycle", m.CyclesStatus})
}
func resultScreens(m ModelsSnapshot) ScreenContent {
	return modelFields(Field{"Results/reproducibility", m.ResultsStatus}, Field{"Cycle", m.CyclesStatus})
}
func rolloutScreens(m ModelsSnapshot) ScreenContent {
	return modelFields(Field{"Canaries", m.CanaryStatus}, Field{"Promotion evidence", m.ResultsStatus})
}
func feedbackScreens(m ModelsSnapshot) ScreenContent {
	return modelFields(Field{"Process 07 feedback", m.FeedbackStatus}, Field{"Cycle", m.CyclesStatus})
}

func localModelsScreen(m ModelsSnapshot) ScreenContent {
	content := ScreenContent{ReadOnly: true}
	if len(m.LocalModels) == 0 {
		content.Fields = []Field{{Label: "Local models", Value: m.LocalStatus}}
		return content
	}
	content.Fields = append(content.Fields, Field{Label: "Available", Value: m.LocalStatus})
	for _, model := range m.LocalModels {
		content.Fields = append(content.Fields,
			// The compatibility carries its own reason, so "MAY_FIT" is never
			// read as an endorsement.
			Field{Label: model.Name.Display(), Value: model.Compatibility})
		if model.Family.Status == TruthKnown {
			content.Fields = append(content.Fields,
				Field{Label: "  family", Value: model.Family})
		}
	}
	return content
}

func optimizationScreen(m ModelsSnapshot) ScreenContent {
	content := ScreenContent{ReadOnly: true}
	content.Fields = append(content.Fields,
		Field{Label: "Cycles", Value: m.CyclesStatus},
		Field{Label: "Canaries", Value: m.CanaryStatus})
	for _, cycle := range m.Cycles {
		content.Fields = append(content.Fields,
			Field{Label: cycle.ID.Display(), Value: cycle.Status},
			Field{Label: "  started", Value: cycle.Started})
	}
	for _, canary := range m.Canaries {
		content.Fields = append(content.Fields,
			Field{Label: canary.ID.Display(), Value: canary.Status},
			Field{Label: "  promotion", Value: canary.Promoted})
	}
	return content
}

// RenderModelsNode returns what a Models node displays, if this build binds it.
func RenderModelsNode(n *Node, m ModelsSnapshot) (ScreenContent, bool) {
	if n == nil {
		return ScreenContent{}, false
	}
	// The two frozen gaps render as gaps wherever they are reached, including
	// through a cross-link from another section.
	if notice, isGap := GapNotice(n); isGap {
		return ScreenContent{ReadOnly: true, HasNotice: true, Notice: notice}, true
	}
	render, ok := modelsScreens()[n.SpecID]
	if !ok {
		return ScreenContent{}, false
	}
	content := render(m)
	content.ReadOnly = isReadOnly(n)
	content = dedicatedLeafContent(n, content, modelsBinding)
	return content, true
}
