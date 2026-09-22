//go:build linux && race

package tui

// ptyPatience stretches every PTY wait when the race detector is on.
//
// The detector costs this suite roughly a factor of four — the store package,
// measured on the same machine, goes from 12 seconds to 400 — and a PTY test
// waits on a second process starting, drawing its first frame and answering a
// keystroke. On a loaded CI runner that arrived after the eight-second window
// and the test reported a missing marker, which reads like a broken TUI rather
// than a slow one.
//
// The multiplier is here rather than in a larger constant so the uninstrumented
// run keeps failing fast: a real hang should not cost half a minute to notice.
const ptyPatience = 4
