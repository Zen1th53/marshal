//go:build marshal_pty_test

package cli

// The PTY harness owns the controlling terminal and tests native input directly.
// Only its explicitly tagged binary bypasses tmux session launch; tmux must
// still be installed. Release builds always launch through tmux.
const directPTYTest = true
