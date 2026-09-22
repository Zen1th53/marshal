package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// The whole space, not a few arrangements someone thought of.
//
// Four agents. Each reader takes any subset of the three others — itself is
// never a choice, because an agent already knows what it did — which is 8
// subsets per reader and 8^4 = 4096 arrangements. Crossed with the 16 subsets
// of participants, 65536 configurations in all.
//
// These are exercised through the text form rather than the disk, so the whole
// space is reachable in a test rather than a sample of it. What is checked is
// that the arrangement written is the arrangement read back, for every one.

// othersOf lists the agents a reader may be configured to take.
func othersOf(reader string) []string {
	var others []string
	for _, candidate := range knownProviders {
		if candidate != reader {
			others = append(others, candidate)
		}
	}
	return others
}

// subsetOf turns a bitmask into the subset it selects.
func subsetOf(all []string, mask int) []string {
	var out []string
	for i, name := range all {
		if mask&(1<<i) != 0 {
			out = append(out, name)
		}
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// Every arrangement survives being written and read back.
//
// A configuration that means one thing in memory and another after a restart
// would be the worst kind of bug here: the operator would have withheld work
// from a model and it would be shown anyway, silently, one session later.
func TestChannelEveryArrangementRoundTrips(t *testing.T) {
	readers := knownProviders
	checked := 0

	for participantMask := 0; participantMask < 1<<len(knownProviders); participantMask++ {
		participants := subsetOf(knownProviders, participantMask)
		for arrangement := 0; arrangement < 1<<(3*len(readers)); arrangement++ {
			cfg := newChannelConfig()
			cfg.participants = participants
			for i, reader := range readers {
				mask := (arrangement >> (3 * i)) & 0b111
				cfg.sees[reader] = subsetOf(othersOf(reader), mask)
			}

			reloaded, problems := parseChannelConfig(formatChannelConfig(cfg))
			if len(problems) != 0 {
				t.Fatalf("participants=%v arrangement=%d reported %v", participants, arrangement, problems)
			}
			for _, reader := range readers {
				for _, author := range knownProviders {
					want, got := cfg.canSee(reader, author), reloaded.canSee(reader, author)
					if want != got {
						t.Fatalf("participants=%v arrangement=%d: %s sees %s = %v after a round trip, was %v",
							participants, arrangement, reader, author, got, want)
					}
				}
				if !sameSet(cfg.visibleTo(reader), reloaded.visibleTo(reader)) {
					t.Fatalf("participants=%v arrangement=%d: %s takes %v after a round trip, was %v",
						participants, arrangement, reader, reloaded.visibleTo(reader), cfg.visibleTo(reader))
				}
			}
			for _, agent := range knownProviders {
				if cfg.joins(agent) != reloaded.joins(agent) {
					t.Fatalf("participants=%v: %s joins = %v after a round trip, was %v",
						participants, agent, reloaded.joins(agent), cfg.joins(agent))
				}
			}
			checked++
		}
	}
	t.Logf("%d arrangements round-tripped", checked)
}

// The invariants that must hold in every arrangement, whatever was configured.
func TestChannelInvariantsHoldInEveryArrangement(t *testing.T) {
	readers := knownProviders
	checked := 0

	for participantMask := 0; participantMask < 1<<len(knownProviders); participantMask++ {
		participants := subsetOf(knownProviders, participantMask)
		for arrangement := 0; arrangement < 1<<(3*len(readers)); arrangement++ {
			cfg := newChannelConfig()
			cfg.participants = participants
			for i, reader := range readers {
				cfg.sees[reader] = subsetOf(othersOf(reader), (arrangement>>(3*i))&0b111)
			}

			for _, reader := range readers {
				// An agent is never shown its own work, in any arrangement.
				if cfg.canSee(reader, reader) {
					t.Fatalf("participants=%v arrangement=%d: %s was shown its own work",
						participants, arrangement, reader)
				}
				for _, author := range knownProviders {
					if !cfg.canSee(reader, author) {
						continue
					}
					// Nothing is visible from an agent that never joined: there
					// is nothing of theirs in the channel to see.
					if !cfg.joins(author) {
						t.Fatalf("participants=%v arrangement=%d: %s sees %s, which is not in the channel",
							participants, arrangement, reader, author)
					}
					// Visibility never exceeds what was configured.
					if !containsProvider(cfg.sees[reader], author) {
						t.Fatalf("participants=%v arrangement=%d: %s sees %s, which it was not given",
							participants, arrangement, reader, author)
					}
				}
			}
			checked++
		}
	}
	t.Logf("%d arrangements checked", checked)
}

// Every filter a reader can have, rendered.
//
// A filter that is right in the configuration and wrong in the rendering would
// show a model exactly what the operator kept from it, so the file is read back
// for all 4 readers × 8 subsets rather than reasoned about.
func TestChannelEveryFilterRendersExactly(t *testing.T) {
	for _, reader := range knownProviders {
		others := othersOf(reader)
		for mask := 0; mask < 1<<len(others); mask++ {
			visible := subsetOf(others, mask)

			root := t.TempDir()
			s, err := openStream(root)
			if err != nil {
				t.Fatal(err)
			}
			for i, author := range knownProviders {
				if _, err := s.append(author, "s",
					msg("MARK-"+author, baseTime.Add(time.Duration(i)*time.Second))); err != nil {
					t.Fatal(err)
				}
			}
			entries, err := s.since(-1)
			if err != nil {
				t.Fatal(err)
			}

			cfg := newChannelConfig()
			cfg.sees[reader] = visible
			view, err := openInboxView(root, reader, true)
			if err != nil {
				t.Fatal(err)
			}
			shown, err := view.deliver(entries, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if shown != len(visible) {
				t.Errorf("%s with %v was shown %d entries, want %d", reader, visible, shown, len(visible))
			}

			data, err := os.ReadFile(inboxPath(root, reader))
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)
			for _, author := range knownProviders {
				want := containsProvider(visible, author)
				got := strings.Contains(content, "MARK-"+author)
				if got != want {
					t.Errorf("%s with %v: %s present = %v, want %v", reader, visible, author, got, want)
				}
			}
		}
	}
}

// Every pair, in both directions, set one at a time.
//
// The directions are independent: withholding A from B must not withhold B
// from A. Twelve ordered pairs, each checked with the other eleven left alone.
func TestChannelEveryDirectionIsIndependent(t *testing.T) {
	for _, reader := range knownProviders {
		for _, author := range othersOf(reader) {
			// Everyone default except this one reader, which drops one author.
			kept := []string{}
			for _, candidate := range othersOf(reader) {
				if candidate != author {
					kept = append(kept, candidate)
				}
			}
			line := fmt.Sprintf("%s: %s\n", reader, strings.Join(kept, ", "))
			if len(kept) == 0 {
				line = fmt.Sprintf("%s: none\n", reader)
			}
			cfg, problems := parseChannelConfig(line)
			if len(problems) != 0 {
				t.Fatalf("%s: %v", line, problems)
			}

			if cfg.canSee(reader, author) {
				t.Errorf("%s still sees %s after it was withheld", reader, author)
			}
			// The opposite direction is untouched.
			if !cfg.canSee(author, reader) {
				t.Errorf("withholding %s from %s also withheld %s from %s",
					author, reader, reader, author)
			}
			// Everyone else is untouched.
			for _, other := range knownProviders {
				for _, source := range knownProviders {
					if other == reader && source == author {
						continue
					}
					want := other != source
					if got := cfg.canSee(other, source); got != want {
						t.Errorf("withholding %s from %s changed %s sees %s to %v",
							author, reader, other, source, got)
					}
				}
			}
		}
	}
}
