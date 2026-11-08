package importer

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSessionEpisodeIDIsStablePerSessionPart(t *testing.T) {
	a := SessionEpisodeID("p", "codex", "s", 0)
	if a != SessionEpisodeID("p", "codex", "s", 0) || !strings.HasPrefix(a, "MEM-IMPORT-") {
		t.Fatal("episode ID not stable")
	}
	for _, other := range []string{SessionEpisodeID("p", "codex", "s", 1), SessionEpisodeID("p", "claude", "s", 0), SessionEpisodeID("q", "codex", "s", 0), SessionEpisodeID("p", "codex", "t", 0)} {
		if other == a {
			t.Fatal("episode IDs collide across part, provider, project or session")
		}
	}
}

func TestBoundEpisodeBodyKeepsOpeningAndLatestWholeTurns(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "[user]: turn %03d é %s\n", i, strings.Repeat("y", 60))
	}
	body := strings.TrimSpace(b.String())
	got := BoundEpisodeBody(body)
	if len(got) > EpisodeMaxBytes || !utf8.ValidString(got) {
		t.Fatalf("bound: %d bytes valid=%v", len(got), utf8.ValidString(got))
	}
	if !strings.HasPrefix(got, "[user]: turn 000") || !strings.HasSuffix(got, "turn 399 é "+strings.Repeat("y", 60)) || strings.Count(got, episodeOmission) != 1 {
		t.Fatalf("bound shape:\n%s", got[:200])
	}
	tailStart := strings.Index(got, episodeOmission) + len(episodeOmission)
	if !strings.HasPrefix(got[tailStart:], "[user]: turn ") {
		t.Fatal("tail does not start at a whole turn")
	}
	// Re-bounding an already bounded body keeps one marker.
	again := BoundEpisodeBody(got + "\n[assistant]: " + strings.Repeat("z", 4000))
	if len(again) > EpisodeMaxBytes || strings.Count(again, episodeOmission) != 1 || !strings.HasPrefix(again, "[user]: turn 000") {
		t.Fatalf("re-bound: %d bytes, %d markers", len(again), strings.Count(again, episodeOmission))
	}
	if short := "[user]: hi"; BoundEpisodeBody(short) != short {
		t.Fatal("short body changed")
	}
}
