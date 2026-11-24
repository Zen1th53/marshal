package hostwatch

import (
	"testing"
	"time"
)

func TestThermalPreferenceWindowAndDisable(t *testing.T) {
	now := time.Now()
	samples := []Sample{{At: now.Add(-20 * time.Second), CPUPackageTempC: 50, CPUPackageTempCKnown: true}, {At: now, CPUPackageTempC: 95, CPUPackageTempCKnown: true}}
	p := DefaultPolicy()
	long, _ := p.Evaluate(samples, State{})
	if long.ActiveRules["thermal_package_temp"] {
		t.Fatal("long window ignored cool sample")
	}
	p.PackageTempWindowSeconds = 10
	short, _ := p.Evaluate(samples, State{})
	if !short.ActiveRules["thermal_package_temp"] {
		t.Fatal("short window did not trigger")
	}
	p.ThermalDisabled = true
	disabled, _ := p.Evaluate(samples, State{})
	if disabled.ActiveRules["thermal_package_temp"] {
		t.Fatal("disabled thermal guard triggered")
	}
}
