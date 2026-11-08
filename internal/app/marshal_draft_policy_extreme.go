//go:build !governed

package app

func marshalDraftTaskPolicy() string {
	return "Every task must use mode governed and have exactly one acceptance criterion. Every executable check must name that criterion verbatim. opencode supports governed when requested; agy also supports governed when requested. Assign either worker only with a current verified governed harness profile for the chosen model. Use only workers with such a profile. Do not propose native tasks."
}
