package v2

import (
	"os"
	"testing"
	"time"
)

// TestMain pins the local zone to UTC: the Board shows every time in local
// time, and the goldens are written with UTC fixtures, so a run on a machine
// in another zone would shift every clock. Tests of the zone itself set
// time.Local for their own run and restore it.
func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}
