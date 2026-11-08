package verification

import (
	"testing"
	"time"
)

func TestWaiverCannotOverrideMandatoryOrCritical(t *testing.T) {
	w := Waiver{ID: "w", Actor: "operator", Reason: "accepted", ExpiresAt: time.Now().Add(time.Hour)}
	if ValidateWaiver(w, true, false, time.Now()) != ErrUnsafeWaiver {
		t.Fatal("mandatory criterion waived")
	}
}
