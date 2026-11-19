package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/adapter"
)

func (r *Runtime) workerProviderError(ctx context.Context, err error) error {
	var process *adapter.ProcessError
	if !errors.As(err, &process) || len(process.Stderr) == 0 {
		return err
	}
	stderr, sanitizeErr := r.sanitizeProviderOutput(ctx, process.Stderr)
	if sanitizeErr != nil {
		return fmt.Errorf("%w; provider stderr rejected by evidence sanitizer", err)
	}
	return fmt.Errorf("%w; %s", err, workerFailureMessage("provider probe failed", stderr, nil))
}

// stderr has already passed the provider output sanitizer. Keep the first
// and last lines in the durable failure and the Board's retry/pause text.
func workerFailureMessage(summary string, stderr, stdout []byte) string {
	if summary == "" {
		return ""
	}
	text := string(stderr) + "\n" + string(stdout)
	if isProviderAuthFailure(text) && !isProviderAuthFailure(summary) {
		summary += "; authentication failed"
	} else if limit := providerLimitReason(stderr, stdout); limit != "" && !lineNamesLimit(strings.ToLower(summary)) {
		summary += "; provider limit: " + limit
	}
	stream, detail := "stderr", string(stderr)
	if strings.TrimSpace(detail) == "" {
		stream, detail = "stdout", providerLimitText(string(stdout))
	}
	if detail := adapter.DiagnosticSummary(detail, 800); detail != "" {
		return summary + "; " + stream + ": " + detail
	}
	return summary
}
