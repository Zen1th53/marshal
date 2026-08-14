package evidence

import (
	"context"
	"errors"
	"testing"
)

func TestStrictSanitizerRejectsOversizedMetadataMap(t *testing.T) {
	metadata := make(map[string]string, 1025)
	for i := 0; i < 1025; i++ {
		metadata["field-"+string(rune(i))] = "value"
	}
	node := Node{ID: "A07-MAP", Type: NodeTypeOutput, Metadata: metadata}
	if _, err := NewStrictSanitizer(SanitizerConfig{}).SanitizeNode(context.Background(), node); !errors.Is(err, ErrSecretRejected) {
		t.Fatalf("error = %v, want %v", err, ErrSecretRejected)
	}
}
