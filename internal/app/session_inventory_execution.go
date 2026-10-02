package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/Zen1th53/marshal/internal/project"
)

// ExecuteNativeCodexConversation uses native storage for a scoped conversation;
// it never substitutes the governed task home for the provider's history home.
func (r *Runtime) ExecuteNativeCodexConversation(ctx context.Context, operation, target string, tail []string) (string, error) {
	if operation != "resume" && operation != "fork" {
		return "", fmt.Errorf("unsupported native conversation operation")
	}
	checkArgs := append([]string{"exec", operation, target}, tail...)
	dialect := ObserveProviderDialect(ctx, "codex")
	if err := dialect.Check(ProviderArgOperation("codex", checkArgs), false); err != nil {
		return "", err
	}
	conversation, err := r.ResolveNativeConversation(ctx, "codex", target)
	if err != nil {
		return "", err
	}
	binary, err := project.FindBinary("codex")
	if err != nil {
		return "", err
	}
	args := append([]string{"exec", operation, conversation.SourceID}, tail...)
	childCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(childCtx, binary, args...)
	cmd.Dir = r.ProjectRoot()
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("native codex %s failed: %w", operation, err)
	}
	result := string(out)
	if dialect.Operation(ProviderArgOperation("codex", args)).Status == ProviderUnknown {
		result = "UNKNOWN — unqualified pass-through: codex " + operation + "\n" + result
	}
	return result, nil
}
