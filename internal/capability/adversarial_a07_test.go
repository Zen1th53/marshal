package capability

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeResourceRejectsTraversalControlAndInvalidUTF8(t *testing.T) {
	for _, resource := range []string{"../outside", "..\\outside", "\x00secret", string([]byte{0xff, 0xfe})} {
		if _, err := NormalizeResource(KindFilesystemWrite, resource); !errors.Is(err, ErrInvalidScope) {
			t.Errorf("resource %q error=%v, want ErrInvalidScope", resource, err)
		}
	}
}

func TestCapabilityValidationDoesNotEchoSecretMarker(t *testing.T) {
	marker := "MARSHAL_TEST_SECRET_T01_A07"
	err := (Scope{Resource: marker, Actions: []string{""}}).Validate()
	if !errors.Is(err, ErrInvalidScope) || strings.Contains(err.Error(), marker) {
		t.Fatalf("validation error=%v leaked marker", err)
	}
}

func FuzzNormalizeResourceNeverPanics(f *testing.F) {
	f.Add("/workspace/task")
	f.Add("../outside")
	f.Fuzz(func(t *testing.T, resource string) {
		_, _ = NormalizeResource(KindFilesystemWrite, resource)
	})
}
