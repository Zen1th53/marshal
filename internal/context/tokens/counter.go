package tokens

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sync"
	"unicode/utf8"
)

type Counter struct {
	mu    sync.RWMutex
	cache map[string]int // key: sha256(modelName + ":" + text)
}

func NewCounter() *Counter {
	return &Counter{
		cache: make(map[string]int),
	}
}

func cacheKey(modelName, text string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s", modelName, text)
	return hex.EncodeToString(h.Sum(nil))
}

// CountTokens estimates or measures token usage in a provider-aware, conservative manner.
func (c *Counter) CountTokens(provider, modelName, text string) int {
	if len(text) == 0 {
		return 0
	}

	key := cacheKey(modelName, text)

	c.mu.RLock()
	cached, ok := c.cache[key]
	c.mu.RUnlock()
	if ok {
		return cached
	}

	runeCount := utf8.RuneCountInString(text)
	byteCount := len(text)

	// Conservative ratio based on provider and text composition
	var charsPerToken float64
	switch provider {
	case "claude", "anthropic":
		charsPerToken = 3.4
	case "openai", "codex":
		charsPerToken = 3.6
	case "gemini", "google":
		charsPerToken = 3.5
	default:
		charsPerToken = 3.2 // conservative fallback
	}

	baseTokens := float64(runeCount) / charsPerToken

	// Multi-byte Unicode penalty (e.g. CJK, emojis): runes with > 1 byte often consume 1-2 tokens per rune
	nonASCIIBytes := byteCount - runeCount
	if nonASCIIBytes > 0 {
		unicodeExtra := float64(nonASCIIBytes) * 0.5
		baseTokens += unicodeExtra
	}

	total := int(math.Ceil(baseTokens))
	if total < 1 && runeCount > 0 {
		total = 1
	}

	c.mu.Lock()
	c.cache[key] = total
	c.mu.Unlock()

	return total
}

// CountMessageOverhead computes provider-specific framing overhead for messages and tool declarations.
func (c *Counter) CountMessageOverhead(provider, modelName string, numMessages, numTools int) int {
	var perMessageOverhead int
	var perToolOverhead int

	switch provider {
	case "openai", "codex":
		perMessageOverhead = 4
		perToolOverhead = 10
	case "claude", "anthropic":
		perMessageOverhead = 5
		perToolOverhead = 12
	default:
		perMessageOverhead = 4
		perToolOverhead = 10
	}

	return (numMessages * perMessageOverhead) + (numTools * perToolOverhead)
}
