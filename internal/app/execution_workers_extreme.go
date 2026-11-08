//go:build !governed

package app

// runtimeMaxWorkers is the execution engine's lane ceiling. Extreme lets the
// Marshal's own per-provider limits and global cap (marshal settings) decide
// how many workers run at once, so the engine ceiling sits above them.
const runtimeMaxWorkers = 32
