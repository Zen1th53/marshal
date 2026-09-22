//go:build linux && !race

package tui

// ptyPatience is the uninstrumented multiplier: the waits are already sized
// for a machine running at full speed. See the race-tagged file for why the
// instrumented run needs more.
const ptyPatience = 1
