package app

// AgyVerifiesNatively reports whether agy is verified with the native probe
// (CLI-owned permissions) rather than the sandboxed one. Every surface that
// verifies a provider uses it, so "verify agy" works wherever agy can run.
func AgyVerifiesNatively() bool { return !privateAGYEnabled() }
