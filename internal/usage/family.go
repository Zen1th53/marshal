package usage

import "strings"

// Some providers report one limit per model family (agy: "Gemini",
// "Claude and GPT", "Gemini 4 Argon", each with a 5-hour and a weekly
// window); others one limit for every model (codex, claude: "5h", "wk").
// A family window is labelled "<family> <kind>". The rule here is the one
// both the dispatcher and the Board use, so a provider the Board calls
// usable for a model is the provider the dispatcher sends that model's task
// to.

// SplitLabel is a window label as its family and its kind: "Claude and GPT
// wk" is ("Claude and GPT", "wk"), "5h" is ("", "5h").
func SplitLabel(label string) (family, kind string) {
	if sp := strings.LastIndex(label, " "); sp >= 0 {
		return label[:sp], label[sp+1:]
	}
	return "", label
}

// KindName is a window kind in plain words: "wk" is "weekly"; "5h" and
// any other length ("90m") stay as they are.
func KindName(kind string) string {
	if kind == "wk" {
		return "weekly"
	}
	return kind
}

// ReasonNotSignedIn is the reason of a provider whose limits report asked
// for authentication instead (agy prints a sign-in URL and waits). Only
// this constant is kept, never what the CLI printed.
const ReasonNotSignedIn = "not signed in"

// agyAsksToSignIn reports whether agy's output is a sign-in prompt.
func agyAsksToSignIn(out []byte) bool {
	s := strings.ToLower(string(out))
	return strings.Contains(s, "authentication required") || strings.Contains(s, "please sign in") || strings.Contains(s, "not signed in")
}

// FamilyCovers reports whether a family window applies to model id: the
// family's name, or one of its " and " parts, is the id's leading words
// ("Claude and GPT" covers claude-opus-5-5-high and gpt-oss-120b; "Gemini"
// covers every gemini-*; "Gemini 4 Argon" covers gemini-4-argon-high).
func FamilyCovers(family, model string) bool { return familyMatch(family, model) > 0 }

// familyMatch is how many characters of model the family names (its longest
// covering part); 0 when it does not cover model.
func familyMatch(family, model string) int {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	if m == "" {
		return 0
	}
	best := 0
	for _, part := range strings.Split(strings.ToLower(family), " and ") {
		slug := strings.Join(strings.Fields(part), "-")
		if slug == "" {
			continue
		}
		if (m == slug || strings.HasPrefix(m, slug+"-") || strings.HasPrefix(m, slug+".")) && len(slug) > best {
			best = len(slug)
		}
	}
	return best
}

// grouped reports whether the quota's windows are per family.
func (q Quota) grouped() bool {
	for _, w := range q.Windows {
		if f, _ := SplitLabel(w.Label); f != "" {
			return true
		}
	}
	return false
}

// ModelWindows is the windows that limit model: every window of a provider
// whose limits are not per family; of one whose limits are, the windows of
// the most specific family that covers model. agy runs a Gemini 4 Argon
// model on its own "Gemini 4 Argon" group when that group is reported, any
// other gemini-* model on "Gemini", claude-* and gpt-* on "Claude and GPT".
// matched is false when the quota is per family and no family covers model
// (or model is ""): the windows then say nothing about it.
func (q Quota) ModelWindows(model string) (wins []Window, matched bool) {
	if !q.grouped() {
		return append([]Window(nil), q.Windows...), true
	}
	best := 0
	for _, w := range q.Windows {
		if f, _ := SplitLabel(w.Label); f != "" {
			best = max(best, familyMatch(f, model))
		}
	}
	if best == 0 {
		return nil, false
	}
	for _, w := range q.Windows {
		if f, _ := SplitLabel(w.Label); f != "" && familyMatch(f, model) == best {
			wins = append(wins, w)
		}
	}
	return wins, true
}

// ModelBinding is the known window closest to its limit among the windows
// that limit model, as Binding is for every window. ok is false when the
// quota is not known, no window limits model, or none of those is known.
func (q Quota) ModelBinding(model string) (Window, bool) {
	wins, matched := q.ModelWindows(model)
	if !matched {
		return Window{}, false
	}
	return Quota{Known: q.Known, Windows: wins}.Binding()
}

// SourceNotReported is the source of a limit the provider's CLI does not
// report: it is shown as not reported, never as a number.
const SourceNotReported = "not reported by the CLI"

// SourceNotChecked is the source of a provider whose limits MARSHAL does not
// read at all.
const SourceNotChecked = "not checked"

// Source is where the quota's figures came from, in a few words; for a quota
// with no figures, why there are none.
func (q Quota) Source() string {
	if q.Known {
		switch q.Provider {
		case "codex":
			return "codex session log"
		case "claude":
			return "claude usage report"
		case "agy":
			return "agy /usage"
		}
		return q.Provider
	}
	switch q.Reason {
	case NotExposed:
		if len(q.Offer) > 0 {
			return "agy models"
		}
		return SourceNotChecked
	case "", ReasonNoFile, ReasonNotReported:
		if len(q.Offer) > 0 {
			return "agy models"
		}
		return SourceNotReported
	case ReasonNotSignedIn:
		return "not signed in"
	case ReasonStale:
		return "last report too old"
	case ReasonReset:
		return "reset since last report"
	}
	return "unreadable: " + q.Reason
}
