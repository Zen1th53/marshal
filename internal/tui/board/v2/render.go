package v2

import "github.com/Zen1th53/marshal/internal/tui/board/vm"

// MinCols and MinRows are the least the v2 screens draw in; below them the
// caller keeps its own narrow screen.
const (
	MinCols = 60
	MinRows = 12
)

// Render paints b.Screen in cols x rows. The last row is left blank for the
// caller's status or composer line. Chat screens draw an empty provider area;
// RenderChat fills it.
func Render(b vm.Board, cols, rows int, th Theme) []string {
	return RenderChat(b, cols, rows, th, nil)
}

// RenderChat is Render with the provider CLI's own lines for the chat
// screens' provider area, drawn as they are.
func RenderChat(b vm.Board, cols, rows int, th Theme, pane []string) []string {
	lines, _ := RenderChatHits(b, cols, rows, th, pane)
	return lines
}

// RenderChatHits is RenderChat with the clickable regions it drew.
func RenderChatHits(b vm.Board, cols, rows int, th Theme, pane []string) ([]string, []Hit) {
	if cols <= 0 || rows <= 0 {
		return nil, nil
	}
	c := newCanvas(cols, rows, th.Unicode)
	switch b.Screen {
	case vm.ScreenReady:
		screenReady(c, b)
	case vm.ScreenPlanning:
		screenPlanning(c, b)
	case vm.ScreenDecision:
		screenDecision(c, b)
	case vm.ScreenPaused:
		screenPaused(c, b)
	case vm.ScreenChat:
		screenChat(c, b, pane)
	case vm.ScreenSplit:
		screenSplit(c, b, pane)
	default:
		screenRunning(c, b)
	}
	return c.lines(th), c.hits
}

// RenderSide paints the compact Board alone, for a Board pane that has the
// Marshal chat beside it in another pane: the top bar, the NOW line, the
// compact panes and the key bar.
func RenderSide(b vm.Board, cols, rows int, th Theme) []string {
	lines, _ := RenderSideHits(b, cols, rows, th)
	return lines
}

// RenderSideHits is RenderSide with the clickable regions it drew.
func RenderSideHits(b vm.Board, cols, rows int, th Theme) ([]string, []Hit) {
	if cols <= 0 || rows <= 0 {
		return nil, nil
	}
	c := newCanvas(cols, rows, th.Unicode)
	topBar(c, 0, b)
	if rows >= 4 {
		keyBar(c, rows-2, keysOr(b, splitKeys))
	}
	side(c, b, 0, cols, 1, max(0, rows-4))
	sideCard(c, b, 0, cols, 2, max(0, rows-5))
	return c.lines(th), c.hits
}
