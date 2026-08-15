package policy

import "testing"

func TestPolicyDigestRejectsMalformedValue(t *testing.T) {

	if err := PolicyDigest("sha256:not-a-digest").Validate(); err == nil {
		t.Fatal("malformed policy digest accepted")
	}
}
