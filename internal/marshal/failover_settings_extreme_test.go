//go:build !governed

package marshal

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestExtremeFailoverSettingsRoundTrip(t *testing.T) {
	s := DefaultSettings()
	s.FailoverOrder = "codex,claude"
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got Settings
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.EffectiveFailoverOrder(), []string{"codex", "claude"}) {
		t.Fatal("order lost")
	}
	got.FailoverOrder = "codex,codex"
	if got.Validate() == nil {
		t.Fatal("duplicate order accepted")
	}
}
