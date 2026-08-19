package tokens_test

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/context/tokens"
)

func TestT114ProviderAwareTokenCounting(t *testing.T) {
	c := tokens.NewCounter()

	// 1. Plain English text
	plainText := "The quick brown fox jumps over the lazy dog."
	countPlain := c.CountTokens("openai", "gpt-4o", plainText)
	if countPlain < 9 || countPlain > 15 {
		t.Fatalf("unexpected token count for plain text: %d", countPlain)
	}

	// 2. Complex code & JSON fixture
	codeFixture := `{"schema_version": 68, "table": "memory_records_v2", "sql": "SELECT id, title, body FROM memory_records_v2 WHERE project_id = ? AND valid_to IS NULL"}`
	countCode := c.CountTokens("claude", "claude-3-5-sonnet", codeFixture)
	if countCode < 30 {
		t.Fatalf("expected >= 30 tokens for code fixture, got %d", countCode)
	}

	// 3. Multi-byte Unicode / CJK characters: must never underestimate
	unicodeFixture := "こんにちは世界！ 🚀 Safe multi-agent governance and token budgeting."
	countUnicode := c.CountTokens("gemini", "gemini-1.5-pro", unicodeFixture)
	if countUnicode < 15 {
		t.Fatalf("expected >= 15 tokens for Unicode fixture, got %d", countUnicode)
	}

	// 4. Message & tool framing overhead
	overhead := c.CountMessageOverhead("openai", "gpt-4o", 5, 2)
	if overhead < 20 {
		t.Fatalf("expected message framing overhead >= 20 tokens, got %d", overhead)
	}

	// 5. Deterministic caching
	cached1 := c.CountTokens("claude", "claude-3-5-sonnet", codeFixture)
	cached2 := c.CountTokens("claude", "claude-3-5-sonnet", codeFixture)
	if cached1 != cached2 {
		t.Fatalf("cached counts do not match: %d != %d", cached1, cached2)
	}
}
