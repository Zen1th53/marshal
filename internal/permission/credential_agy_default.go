//go:build governed || !linux

package permission

func privateCredentialDisplayName(string) (string, bool) { return "", false }
