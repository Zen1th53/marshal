package tui

import (
	"github.com/rivo/uniseg"
)

// Grapheme segmentation for the line editor.
//
// Two distinct concepts are kept apart on purpose:
//
//   - Segmentation decides what one "character" is to a user, and therefore how
//     far Left/Right move and how much Backspace removes. A family emoji is a
//     single grapheme even though it is seven runes.
//   - Display width decides how many terminal cells that grapheme occupies, and
//     therefore where the hardware cursor goes. A CJK ideograph is one grapheme
//     but two cells.
//
// Editing by rune index conflates the two: it splits combining marks off their
// base, halves a regional-indicator flag, strips a skin-tone modifier and leaves
// a dangling zero-width joiner. All of those were observed before this layer
// existed.

// GraphemeBoundaries returns the rune offsets at which grapheme clusters begin,
// always including 0 and len(runes) so the result brackets the whole buffer.
//
// The returned slice is therefore the set of legal cursor positions: a cursor
// resting anywhere else would sit inside a user-visible character.
func GraphemeBoundaries(runes []rune) []int {
	bounds := []int{0}
	if len(runes) == 0 {
		return bounds
	}

	g := uniseg.NewGraphemes(string(runes))
	offset := 0
	for g.Next() {
		offset += len([]rune(g.Str()))
		bounds = append(bounds, offset)
	}

	// Guard against any tail the segmenter did not report.
	if bounds[len(bounds)-1] != len(runes) {
		bounds = append(bounds, len(runes))
	}
	return bounds
}

// PrevGraphemeStart returns the rune index one whole grapheme before cursor.
// It is where the cursor lands on Left, and what Backspace deletes back to.
func PrevGraphemeStart(runes []rune, cursor int) int {
	if cursor <= 0 {
		return 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}

	prev := 0
	for _, b := range GraphemeBoundaries(runes) {
		if b >= cursor {
			break
		}
		prev = b
	}
	return prev
}

// NextGraphemeStart returns the rune index one whole grapheme after cursor.
// It is where the cursor lands on Right, and what Delete removes forward to.
func NextGraphemeStart(runes []rune, cursor int) int {
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(runes) {
		return len(runes)
	}

	for _, b := range GraphemeBoundaries(runes) {
		if b > cursor {
			return b
		}
	}
	return len(runes)
}

// SnapToGraphemeBoundary moves an arbitrary rune index back to the start of the
// grapheme containing it. Callers that compute a position by other means (word
// motion, history recall, a completion insert) use this so the cursor can never
// come to rest inside a character.
func SnapToGraphemeBoundary(runes []rune, cursor int) int {
	if cursor <= 0 {
		return 0
	}
	if cursor >= len(runes) {
		return len(runes)
	}

	last := 0
	for _, b := range GraphemeBoundaries(runes) {
		if b > cursor {
			return last
		}
		last = b
	}
	return last
}

// GraphemeCount reports how many user-visible characters a string contains.
func GraphemeCount(s string) int {
	if s == "" {
		return 0
	}
	n := 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		n++
	}
	return n
}
