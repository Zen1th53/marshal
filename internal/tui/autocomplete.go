package tui

import (
	"sort"
	"strings"
)

// CompletionContext supplies live runtime objects for tab completion.
type CompletionContext struct {
	Commands     []string // slash commands e.g. ["/status", "/rollback", "/route", ...]
	Agents       []string // real active agent names e.g. ["claude", "codex", "opencode"]
	Claims       []string // live claim IDs e.g. ["C-01", "C-02"]
	Evidence     []string // live evidence IDs e.g. ["E-01", "E-02"]
	Tasks        []string // live task IDs e.g. ["T-01", "T-02"]
	Checkpoints  []string // live checkpoint IDs e.g. ["CP-01", "CP-02"]
	Subcommands  map[string][]string
	Models       []string
}

// Completer manages contextual Tab completion.
type Completer struct {
	ctx           CompletionContext
	activeMatches []string
	matchIndex    int
	lastWord      string
	prefix        string
}

// NewCompleter returns a new Completer with an initial context.
func NewCompleter(ctx CompletionContext) *Completer {
	if ctx.Subcommands == nil {
		ctx.Subcommands = make(map[string][]string)
	}
	return &Completer{
		ctx:        ctx,
		matchIndex: -1,
	}
}

// UpdateContext refreshes live objects for autocomplete.
func (c *Completer) UpdateContext(ctx CompletionContext) {
	c.ctx = ctx
	if c.ctx.Subcommands == nil {
		c.ctx.Subcommands = make(map[string][]string)
	}
}

// Reset clears any active completion cycling.
func (c *Completer) Reset() {
	c.activeMatches = nil
	c.matchIndex = -1
	c.lastWord = ""
	c.prefix = ""
}

// Complete takes the full text and cursor index, and returns the new text, new cursor index,
// and whether any completion was made.
// reverse: if true (Shift+Tab), cycles backward through options.
func (c *Completer) Complete(text string, cursor int, reverse bool) (string, int, bool) {
	runes := []rune(text)
	if cursor < 0 || cursor > len(runes) {
		cursor = len(runes)
	}

	// Find the word boundary before the cursor
	wordStart := cursor
	for wordStart > 0 && !isWordSeparator(runes[wordStart-1]) {
		wordStart--
	}
	currentWord := string(runes[wordStart:cursor])

	// If we are currently cycling through matches for this position
	if len(c.activeMatches) > 0 && c.matchIndex >= 0 && (currentWord == c.lastWord || currentWord == c.prefix) {
		if reverse {
			c.matchIndex--
			if c.matchIndex < 0 {
				c.matchIndex = len(c.activeMatches) - 1
			}
		} else {
			c.matchIndex = (c.matchIndex + 1) % len(c.activeMatches)
		}
		replacement := c.activeMatches[c.matchIndex]
		c.lastWord = replacement

		newRunes := append(append(runes[:wordStart], []rune(replacement)...), runes[cursor:]...)
		newCursor := wordStart + len([]rune(replacement))
		return string(newRunes), newCursor, true
	}

	// Otherwise, find new matches
	matches := c.findMatches(text, cursor, wordStart, currentWord)
	if len(matches) == 0 {
		c.Reset()
		return text, cursor, false
	}

	c.activeMatches = matches
	c.prefix = currentWord
	if reverse {
		c.matchIndex = len(matches) - 1
	} else {
		c.matchIndex = 0
	}

	replacement := c.activeMatches[c.matchIndex]
	if len(matches) == 1 {
		// Append space for single completed token
		replacement += " "
	}
	c.lastWord = replacement

	newRunes := append(append(runes[:wordStart], []rune(replacement)...), runes[cursor:]...)
	newCursor := wordStart + len([]rune(replacement))
	return string(newRunes), newCursor, true
}

func isWordSeparator(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

func (c *Completer) findMatches(fullText string, cursor, wordStart int, word string) []string {
	if word == "" {
		return nil
	}

	var candidates []string

	// 1. Agent mention: starts with @
	if strings.HasPrefix(word, "@") {
		agentPrefix := strings.TrimPrefix(word, "@")
		for _, ag := range c.ctx.Agents {
			if strings.HasPrefix(strings.ToLower(ag), strings.ToLower(agentPrefix)) {
				candidates = append(candidates, "@"+ag)
			}
		}
		if strings.HasPrefix(strings.ToLower("team"), strings.ToLower(agentPrefix)) {
			candidates = append(candidates, "@team")
		}
		sort.Strings(candidates)
		return candidates
	}

	// 2. Object reference: starts with # or direct ID prefixes (C-, E-, T-, CP-)
	trimmedRef := strings.TrimPrefix(word, "#")
	hasHash := strings.HasPrefix(word, "#")

	if strings.HasPrefix(strings.ToUpper(trimmedRef), "C-") {
		for _, id := range c.ctx.Claims {
			if strings.HasPrefix(strings.ToUpper(id), strings.ToUpper(trimmedRef)) {
				if hasHash {
					candidates = append(candidates, "#"+id)
				} else {
					candidates = append(candidates, id)
				}
			}
		}
	} else if strings.HasPrefix(strings.ToUpper(trimmedRef), "E-") {
		for _, id := range c.ctx.Evidence {
			if strings.HasPrefix(strings.ToUpper(id), strings.ToUpper(trimmedRef)) {
				if hasHash {
					candidates = append(candidates, "#"+id)
				} else {
					candidates = append(candidates, id)
				}
			}
		}
	} else if strings.HasPrefix(strings.ToUpper(trimmedRef), "T-") {
		for _, id := range c.ctx.Tasks {
			if strings.HasPrefix(strings.ToUpper(id), strings.ToUpper(trimmedRef)) {
				if hasHash {
					candidates = append(candidates, "#"+id)
				} else {
					candidates = append(candidates, id)
				}
			}
		}
	} else if strings.HasPrefix(strings.ToUpper(trimmedRef), "CP-") {
		for _, id := range c.ctx.Checkpoints {
			if strings.HasPrefix(strings.ToUpper(id), strings.ToUpper(trimmedRef)) {
				if hasHash {
					candidates = append(candidates, "#"+id)
				} else {
					candidates = append(candidates, id)
				}
			}
		}
	}

	if len(candidates) > 0 {
		sort.Strings(candidates)
		return candidates
	}

	// 3. Subcommands if we have a preceding command
	beforeWord := strings.TrimSpace(fullText[:wordStart])
	parts := strings.Fields(beforeWord)
	if len(parts) >= 1 && strings.HasPrefix(parts[0], "/") {
		cmd := parts[0]
		if subcmds, ok := c.ctx.Subcommands[cmd]; ok {
			for _, sc := range subcmds {
				if strings.HasPrefix(strings.ToLower(sc), strings.ToLower(word)) {
					candidates = append(candidates, sc)
				}
			}
			if len(candidates) > 0 {
				sort.Strings(candidates)
				return candidates
			}
		}
	}

	// 4. Slash commands: starts with /
	if strings.HasPrefix(word, "/") {
		lowerWord := strings.ToLower(word)
		// Exact prefix matches first
		for _, cmd := range c.ctx.Commands {
			if strings.HasPrefix(strings.ToLower(cmd), lowerWord) {
				candidates = append(candidates, cmd)
			}
		}
		// Fuzzy / abbreviation matches if no prefix matches (e.g., /rb -> /rollback)
		if len(candidates) == 0 {
			abbr := strings.TrimPrefix(lowerWord, "/")
			for _, cmd := range c.ctx.Commands {
				cleanCmd := strings.TrimPrefix(strings.ToLower(cmd), "/")
				if isSubsequence(abbr, cleanCmd) {
					candidates = append(candidates, cmd)
				}
			}
		}
		sort.Strings(candidates)
		return candidates
	}

	return nil
}

func isSubsequence(sub, str string) bool {
	if sub == "" {
		return true
	}
	sIdx := 0
	subRunes := []rune(sub)
	for _, r := range str {
		if r == subRunes[sIdx] {
			sIdx++
			if sIdx == len(subRunes) {
				return true
			}
		}
	}
	return false
}

// ActiveMatches returns the currently active matches being cycled through.
func (c *Completer) ActiveMatches() []string {
	return c.activeMatches
}
