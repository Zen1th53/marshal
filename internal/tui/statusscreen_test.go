package tui

import "testing"

// Gemini was replaced by Antigravity (agy) as a provider. The frozen Status
// leaf CTUI-0139 must no longer ask for "Gemini" — a provider that does not
// exist — nor duplicate CTUI-0141; it reports that Gemini was replaced. The
// other provider leaves keep their own keys and order.
func TestExactHomeStatusValueReportsRealProvidersOnly(t *testing.T) {
	row := func(name, availability string) ProviderRow {
		return ProviderRow{
			Name:         Known(name, providerSource),
			Availability: Known(availability, providerSource),
		}
	}
	snap := Snapshot{Providers: ProviderList{
		Rows: []ProviderRow{
			row("Claude", "claude-availability"),
			row("Codex", "codex-availability"),
			row("OpenCode", "opencode-availability"),
			row("Antigravity", "antigravity-availability"),
		},
		Status: Known("4 providers", providerSource),
	}}

	retired, ok := exactHomeStatusValue("CTUI-0139", snap)
	if !ok || retired.Status != TruthNotRun || retired.Text != "" {
		t.Fatalf("CTUI-0139 = %+v, want a NOT RUN value naming the Antigravity replacement", retired)
	}

	cases := []struct {
		spec string
		want string
	}{
		{"CTUI-0137", "claude-availability"},
		{"CTUI-0138", "codex-availability"},
		{"CTUI-0140", "opencode-availability"},
		{"CTUI-0141", "antigravity-availability"},
	}
	for _, tc := range cases {
		v, ok := exactHomeStatusValue(tc.spec, snap)
		if !ok {
			t.Fatalf("%s did not resolve to an exact provider value", tc.spec)
		}
		if v.Text != tc.want {
			// A lookup against a provider absent from the list (such as
			// "Gemini") falls through to the list status instead of matching
			// a row, so this assert also proves CTUI-0139 no longer asks for
			// the retired provider.
			t.Fatalf("%s reported %q, want %q", tc.spec, v.Text, tc.want)
		}
	}
}
