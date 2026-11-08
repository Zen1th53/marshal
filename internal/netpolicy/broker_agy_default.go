//go:build governed || !linux

package netpolicy

func privateNativeBroker(provider, model string) (*CredentialBroker, bool, error) {
	return nil, false, nil
}
func privateCredentialProfile(provider, model string) (string, string, string) { return "", "", "" }
