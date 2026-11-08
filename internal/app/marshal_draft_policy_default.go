//go:build governed

package app

func marshalDraftTaskPolicy() string {
	agyMode := "agy uses native"
	if privateAGYEnabled() {
		agyMode = "agy also supports governed when requested and admitted by a current verified profile"
	}
	return "Use mode governed for codex and claude unless the operator explicitly requests native; " + agyMode + "; opencode defaults to native and also supports governed when requested. Honour a goal that requests governed work."
}
