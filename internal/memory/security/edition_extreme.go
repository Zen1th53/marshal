//go:build !governed

package security

// RedactsSecrets: the private edition never stops work for a secret in
// content. It removes the secret, records that it did, and continues.
const RedactsSecrets = true
