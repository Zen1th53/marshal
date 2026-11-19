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
	// A long stderr is shortened in its middle, where a provider's ERROR
	// lines usually are. Name them before the shortened stream, so the
	// failure is classified and shown by them instead of as a crash.
	if line := marshalToolsFailureLine(stderr); line != "" {
		summary += "; " + marshalToolsLostMarker + ": " + adapter.DiagnosticSummary(line, 300)
	} else if lines := stderrErrorLines(stderr, 3); len(lines) > 0 {
		summary += "; errors: " + adapter.DiagnosticSummary(strings.Join(lines, " | "), 400)
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

// marshalToolsLostMarker names, in a worker failure, a broken connection
// between the worker and Marshal's MCP tools. It is lowercase because the
// failure classifier matches lowercased text.
const marshalToolsLostMarker = "lost connection to marshal's tools"

// marshalToolsFailureLine is the stderr line in which the worker's MCP
// client reports that its connection to Marshal's tools failed. Marshal's
// MCP server is a stdio child of the worker (sessionmcp.Configure), so a
// failure of an HTTP MCP client ("HttpRequest") belongs to another, online
// tool server and is not reported here.
func marshalToolsFailureLine(stderr []byte) string {
	for _, line := range strings.Split(string(stderr), "\n") {
		lower := strings.ToLower(line)
		named := strings.Contains(lower, "mcp client for `marshal`") || strings.Contains(lower, "mcp server `marshal`") || strings.Contains(lower, "mcp_servers.marshal")
		transport := strings.Contains(lower, "rmcp::transport") || strings.Contains(lower, "transport channel closed")
		if named && (transport || strings.Contains(lower, "fail") || strings.Contains(lower, "error") || strings.Contains(lower, "timed out")) {
			return strings.TrimSpace(line)
		}
		if transport && !strings.Contains(lower, "httprequest") && (strings.Contains(lower, "fatal") || strings.Contains(lower, "error")) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// stderrErrorLines are the last max distinct lines of stderr that report an
// error or a fatal condition.
func stderrErrorLines(stderr []byte, max int) []string {
	var lines []string
	seen := map[string]bool{}
	all := strings.Split(string(stderr), "\n")
	for i := len(all) - 1; i >= 0 && len(lines) < max; i-- {
		line := strings.TrimSpace(all[i])
		lower := strings.ToLower(line)
		if line == "" || seen[line] {
			continue
		}
		if strings.Contains(line, " ERROR ") || strings.HasPrefix(line, "ERROR ") || strings.HasPrefix(lower, "error:") || strings.Contains(lower, "fatal") {
			seen[line] = true
			lines = append(lines, line)
		}
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return lines
}
