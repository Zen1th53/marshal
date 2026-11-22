//go:build !governed

package app

func marshalDraftTaskPolicy() string {
	workers := "opencode supports governed when requested; agy also supports governed when requested. Assign either worker only with a current verified governed harness profile for the chosen model."
	if !privateAGYEnabled() {
		workers = "opencode supports governed when requested; agy cannot take tasks on this host: do not assign it. Assign opencode only with a current verified governed harness profile for the chosen model."
	}
	return "Every task must use mode governed and have exactly one acceptance criterion. Every executable check must name that criterion verbatim. " + workers + " Use only workers with such a profile. Do not propose native tasks."
}

// draftAssignableWorker reports whether a draft task may name worker in
// this edition. Every task here is governed, and agy runs governed only on
// private Linux: elsewhere the draft is refused for a governed agy task and
// the canonical plan for a native one, so agy is not offered at all.
func draftAssignableWorker(worker string) bool {
	return worker != "agy" || privateAGYEnabled()
}
