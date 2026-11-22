//go:build !governed

package app

func marshalDraftTaskPolicy() string {
	agy := " agy also supports governed when requested."
	if !privateAGYEnabled() {
		agy = " agy cannot take tasks on this host: do not assign it."
	}
	return "Every task must use mode governed and have exactly one acceptance criterion. Every executable check must name that criterion verbatim. opencode supports governed when requested;" + agy + " Assign a worker only with a current verified governed harness profile for the chosen model. Use only workers with such a profile. Do not propose native tasks."
}

// draftAssignableWorker reports whether a draft task may name worker in
// this edition. Every task here is governed, and agy runs governed only on
// private Linux: elsewhere the draft is refused for a governed agy task and
// the canonical plan for a native one, so agy is not offered at all.
func draftAssignableWorker(worker string) bool {
	return worker != "agy" || privateAGYEnabled()
}
