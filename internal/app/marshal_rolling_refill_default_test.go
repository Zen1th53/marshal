//go:build governed

package app

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

func TestGovernedEditionKeepsBatchDispatch(t *testing.T) {
	s := &MarshalService{LaunchSupervisor: &driver.LaunchSupervisor{}}
	if s.rollingRefillAvailable() || s.rollingRefill("run", marshalBrief, []MarshalDispatch{{Tier: marshal.Ultra}}) != nil {
		t.Fatal("governed edition offered rolling refill")
	}
}
