package execution

import (
	"fmt"
	"strings"
)

// DetectHostileLogInjection inspects output for fake system prompt headers or control injections.
func DetectHostileLogInjection(text string) (bool, string) {
	lower := strings.ToLower(text)
	dangerousPrefixes := []string{
		"system: override",
		"ignore all previous instructions",
		"marshal_override:",
		"admin_approval_bypass:",
		"<|im_start|>",
		"<|im_end|>",
	}

	for _, p := range dangerousPrefixes {
		if strings.Contains(lower, p) {
			return true, fmt.Sprintf("detected prompt injection pattern %q in tool output", p)
		}
	}
	return false, ""
}
