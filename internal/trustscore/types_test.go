package trustscore

import "testing"

func TestComponentStruct(t *testing.T) {
	c := Component{Name: "quorum", Score: 95.0, Weight: 1.0}
	if c.Name != "quorum" {
		t.Fatalf("expected quorum, got %s", c.Name)
	}
}
