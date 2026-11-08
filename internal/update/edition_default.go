//go:build governed

package update

// PrivateEdition is false in Community: updates use the public release feed.
func PrivateEdition() bool { return false }

// PrivateGuidance is empty in Community.
const PrivateGuidance = ""
