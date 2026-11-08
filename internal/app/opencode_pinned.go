package app

// PinnedOpenCodeVersion names the OpenCode version the isolated offline
// bootstrap was prepared for, or "" when this edition pins none.
func PinnedOpenCodeVersion() string { return privateOpenCodeVersion() }
