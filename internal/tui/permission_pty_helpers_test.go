//go:build linux

package tui

import (
	"regexp"
	"testing"
)

func grantPTYRead(t *testing.T, s *ptySession, path string) {
	t.Helper()
	s.sendLine("/permission read allow " + path)
	s.mustSee("Read decision recorded: " + path)
}
func approvePTYMemory(t *testing.T, s *ptySession) {
	t.Helper()
	s.sendLine("/memory review")
	s.mustSee("MEM-IMPORT-")
	ids := regexp.MustCompile(`MEM-IMPORT-[0-9a-f]{16}`).FindAllString(s.output(), -1)
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		s.sendLine("/memory allow " + id)
		s.mustSee("Memory decision recorded: " + id)
	}
}
