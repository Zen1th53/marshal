//go:build governed

package statepath

const DirName = ".marshal"
const Name = "marshal"
const OtherDirName = ".marshal-extreme"

const PublicEngine = false

// RefName preserves stored Git branch and checkpoint identities across namespace migration.
const RefName = "marshal"
