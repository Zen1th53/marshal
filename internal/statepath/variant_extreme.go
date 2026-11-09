//go:build !governed

package statepath

const DirName = ".marshal"
const Name = "marshal"
const OtherDirName = ".marshal-extreme"

const PublicEngine = true

// RefName preserves stored Git branch and checkpoint identities across namespace migration.
const RefName = "marshal"

// LegacyRefName is the branch prefix runs created by v0.2.1 and earlier
// public builds used; their stored task branches stay valid.
const LegacyRefName = "marshal-extreme"
