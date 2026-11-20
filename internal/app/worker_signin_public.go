package app

// WorkerSigninInstruction is what the owner runs to sign a worker provider
// in again, in the words a worker-auth pause uses ("run codex login (or set
// CODEX_HOME)"). The Board's paused card shows it so the owner knows what to
// run before pressing Enter.
func WorkerSigninInstruction(provider string) string { return workerSigninInstruction(provider) }
