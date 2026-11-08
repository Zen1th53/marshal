package verification

import (
	"fmt"
	"strings"
	"time"
)

// ValidateWaiver is used by the canonical verification action path.
func ValidateWaiver(w Waiver, mandatory, critical bool, now time.Time) error {
	if mandatory || critical || w.Critical {
		return ErrUnsafeWaiver
	}
	if w.ID == "" || w.Actor == "" || strings.TrimSpace(w.Reason) == "" || !now.Before(w.ExpiresAt) {
		return fmt.Errorf("%w: waiver", ErrInvalid)
	}
	return nil
}
