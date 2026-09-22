package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Who joins the channel, and who each of them can see in it.
//
// Two separate decisions, both made before the work starts.
//
// Joining is about capture: an agent that joins has what it does dropped into
// the shared channel. Seeing is about reading: each agent takes only the
// entries whose author is in its own list.
//
// They are separate because the reason for each is different. An agent might
// join and be read by everyone while itself reading almost nothing, and that is
// a deliberate arrangement rather than a gap. Models differ in what they can
// use: a strong one is better for seeing everything the others did, and a
// weaker one is worse, because context it cannot follow is context it can be
// confused by. The operator decides per agent, which is why the list is per
// reader rather than a single switch.
//
// The file, one line per agent:
//
//	participants: claude, codex, opencode, antigravity
//	agy: all                  # every other agent
//	claude: all
//	codex: claude             # only claude's work
//	opencode: none            # joins the channel, reads nothing from it
//
// An agent with no line reads every other agent. With no participants line,
// every agent MARSHAL runs joins.
//
// An agent is never shown its own entries, and that is not configurable. It
// already knows what it did — the work is its own conversation — so returning
// it would be noise at best, and at worst a model reading its own output back
// as though another agent had reported it.

func livePeerPath(root string) string {
	return filepath.Join(root, ".marshal", "live-peers")
}

// knownProviders is every agent MARSHAL can run in a project.
var knownProviders = []string{"claude", "codex", "opencode", "antigravity"}

// providerAliases maps what an operator types to the name MARSHAL records.
// "agy" is the command; "antigravity" is the provider.
var providerAliases = map[string]string{"agy": "antigravity"}

func canonicalProvider(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if canonical, ok := providerAliases[name]; ok {
		return canonical
	}
	return name
}

// liveCaptureProviders are the agents whose work reaches the channel while they
// are still running.
//
// Claude and Codex write append-only history, which MARSHAL reads as it grows.
//
// Antigravity keeps a SQLite database per conversation. MARSHAL opens those
// read-only and never writes to them, and SQLite in WAL mode serves readers
// while a writer holds the file — measured against a copy of a real agy
// database: 394 reads against a live writer, none blocked, the reader within
// two rows of the writer throughout. listAntigravityConversations already
// stamps the write-ahead log alongside the database, because a conversation's
// newest steps live there before a checkpoint, so a poll sees them.
//
// OpenCode keeps a SQLite store of its own, read the same way and under the
// same guard. Its public CLI export remains what runs at exit and stays the
// source of record; the live read exists so its work reaches the channel while
// it is still working. If the store is not the shape MARSHAL reads, the live
// path stands down and says so, and the export at exit still runs.
//
// Every agent MARSHAL runs now reaches the channel as it works. The list is
// kept as a list rather than collapsed into "all", because whether a provider
// can be read mid-session is a property of that provider and the next one added
// has to earn its place here.
var liveCaptureProviders = []string{"claude", "codex", "antigravity", "opencode"}

func isKnownProvider(p string) bool { return containsProvider(knownProviders, p) }
func capturesLive(p string) bool    { return containsProvider(liveCaptureProviders, p) }

func containsProvider(list []string, provider string) bool {
	for _, candidate := range list {
		if candidate == provider {
			return true
		}
	}
	return false
}

// channelConfig is who joins the channel and who each reader sees in it.
type channelConfig struct {
	// participants is the set that joins. Empty means every agent MARSHAL runs.
	participants []string
	// sees maps a reader to the authors it takes. A reader absent from the map
	// takes everyone.
	sees map[string][]string
}

func newChannelConfig() channelConfig {
	return channelConfig{sees: map[string][]string{}}
}

// joins reports whether an agent's work reaches the channel at all.
func (c channelConfig) joins(provider string) bool {
	if !isKnownProvider(provider) {
		return false
	}
	if len(c.participants) == 0 {
		return true
	}
	return containsProvider(c.participants, provider)
}

// visibleTo returns the authors a reader takes from the channel, sorted.
//
// Never itself: see the note at the top of this file.
func (c channelConfig) visibleTo(reader string) []string {
	if configured, ok := c.sees[reader]; ok {
		return configured
	}
	var all []string
	for _, candidate := range knownProviders {
		if candidate != reader && c.joins(candidate) {
			all = append(all, candidate)
		}
	}
	return all
}

// canSee reports whether a reader takes entries written by an author.
func (c channelConfig) canSee(reader, author string) bool {
	if reader == author || !c.joins(author) {
		return false
	}
	return containsProvider(c.visibleTo(reader), author)
}

func loadChannelConfig(root string) (channelConfig, []string) {
	data, err := os.ReadFile(livePeerPath(root))
	if err != nil {
		return newChannelConfig(), nil
	}
	return parseChannelConfig(string(data))
}

func parseChannelConfig(text string) (channelConfig, []string) {
	cfg := newChannelConfig()
	var problems []string
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, list, found := strings.Cut(line, ":")
		name = canonicalProvider(name)
		if !found || name == "" {
			problems = append(problems, fmt.Sprintf("line %d: expected \"agent: agents\"", i+1))
			continue
		}
		if name == "participants" {
			joined, bad := parseProviderList("", list)
			for _, unknown := range bad {
				problems = append(problems, fmt.Sprintf("line %d: %q is not an agent MARSHAL runs", i+1, unknown))
			}
			cfg.participants = joined
			continue
		}
		if !isKnownProvider(name) {
			problems = append(problems, fmt.Sprintf("line %d: %q is not an agent MARSHAL runs", i+1, name))
			continue
		}
		authors, bad := parseProviderList(name, list)
		for _, unknown := range bad {
			problems = append(problems, fmt.Sprintf("line %d: %q is not an agent MARSHAL runs", i+1, unknown))
		}
		// A line naming only unknown agents is a typo, not a request to read
		// nothing. Recording it as an empty list would turn the mistake into a
		// decision, and would quietly undo whatever an earlier line said.
		if len(authors) == 0 && len(bad) > 0 && !mentionsNone(list) {
			continue
		}
		cfg.sees[name] = authors
	}
	return cfg, problems
}

// parseProviderList reads "all", "none" and agent names.
//
// The reader's own name is accepted and dropped rather than rejected: writing
// it is a harmless misunderstanding of what the channel carries, not a mistake
// worth refusing a line over. visibleTo would exclude it anyway.
func parseProviderList(self, list string) ([]string, []string) {
	var names, bad []string
	for _, raw := range splitList(list) {
		field := canonicalProvider(raw)
		switch {
		case field == "none":
			return nil, bad
		case field == "all":
			for _, candidate := range knownProviders {
				if candidate != self {
					names = append(names, candidate)
				}
			}
		case field == self:
			// dropped, see above
		case !isKnownProvider(field):
			bad = append(bad, raw)
		default:
			names = append(names, field)
		}
	}
	return dedupeSorted(names), bad
}

func mentionsNone(list string) bool {
	for _, field := range splitList(list) {
		if field == "none" {
			return true
		}
	}
	return false
}

func splitList(list string) []string {
	return strings.FieldsFunc(strings.ToLower(list), func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
}

func dedupeSorted(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	out := names[:1]
	for _, name := range names[1:] {
		if name != out[len(out)-1] {
			out = append(out, name)
		}
	}
	return out
}

// saveChannelConfig writes the arrangement to the project.
func saveChannelConfig(root string, cfg channelConfig) error {
	path := livePeerPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(formatChannelConfig(cfg)), 0600)
}

// formatChannelConfig renders every agent's line, so the file shows the whole
// arrangement rather than the part that differs from a default the reader has
// to know about.
//
// Separate from the write so the round trip can be exercised without a disk:
// what parseChannelConfig reads back has to mean exactly what was written, for
// every arrangement rather than the ones someone thought to try.
func formatChannelConfig(cfg channelConfig) string {
	var b strings.Builder
	b.WriteString("# The shared channel: who joins it, and who each agent sees in it.\n")
	b.WriteString("# participants: the agents whose work is dropped into the channel.\n")
	b.WriteString("# <agent>: the authors that agent takes — names, self, all, or none.\n\n")
	participants := cfg.participants
	if len(participants) == 0 {
		participants = knownProviders
	}
	fmt.Fprintf(&b, "participants: %s\n\n", strings.Join(participants, ", "))
	for _, reader := range knownProviders {
		authors := cfg.visibleTo(reader)
		if len(authors) == 0 {
			fmt.Fprintf(&b, "%s: none\n", reader)
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", reader, strings.Join(authors, ", "))
	}
	return b.String()
}
