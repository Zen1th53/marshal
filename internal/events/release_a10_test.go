package events

import (
	"os"
	"strings"
	"testing"
)

func TestT43A10OperatorDocsNameStableErrorsAndEvents(t *testing.T) {
	body, err := os.ReadFile("../../docs/structured-events.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"EVENT_TYPE_INVALID",
		"EVENT_SECRET_FIELD",
		"EVENT_STORE_FAILED",
		"EVENT_SEQUENCE_CONFLICT",
		"events.appended",
		"events.subscriber.dropped",
		"events.schema.rejected",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("operator docs missing %q", required)
		}
	}
}
