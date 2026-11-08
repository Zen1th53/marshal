//go:build !governed

package app

import "testing"

func TestRequiredContextMarshalResultRejectsAmbiguousJSON(t *testing.T) {
	for _, raw := range []string{
		`{"ok":true,"ok":false}`,
		`{"OK":true}`,
		`{"ok":true,"OK":false}`,
		`{"nested":{"value":true,"value":false}}`,
		`{"nested":{"VALUE":true}}`,
		`{"ok":true}{"ok":false}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var out struct {
				OK     bool `json:"ok"`
				Nested struct {
					Value bool `json:"value"`
				} `json:"nested"`
			}
			if err := (&MarshalCLI{}).decodeRequiredMarshalResult([]byte(raw), &out); err == nil || out.OK || out.Nested.Value {
				t.Fatal("ambiguous result adopted", err, out)
			}
		})
	}
}
