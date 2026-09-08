package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestReviewCommandIsRegisteredAndFailClosed(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), ".", []string{"review"}, strings.NewReader(""), &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "marshal review start") {
		t.Fatalf("code=%d output=%q", code, errOut.String())
	}
}
