package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Who sees whom, live.
//
// Cross-agent delivery used to be fixed: every provider whose history can be
// tailed fed every other one. That is the right default and the wrong rule —
// an operator running a reviewer alongside an implementer may not want the
// implementer's every tool call arriving in the reviewer's inbox, and the
// reverse is a different decision again.
//
// The matrix is stored per project as one line per receiver:
//
//	claude: codex
//	codex: none
//
// A receiver with no line takes the default, which is every eligible sender.
// "none" is written explicitly, so silence in the file never has to be read as
// a decision someone made.

// livePeerPath is the per-project matrix.
func livePeerPath(root string) string {
	return filepath.Join(root, ".marshal", "live-peers")
}

// liveSenders lists the providers whose work can be delivered as it happens.
//
// Codex and Claude write append-only history files, which are safe to read
// while the process owning them runs. OpenCode exposes history through a CLI
// export backed by its live database, and Antigravity holds per-conversation
// SQLite open, so both are imported when their own process exits. Until that
// changes there is nothing to deliver from them mid-session.
var liveSenders = []string{"codex", "claude"}

// liveReceivers lists the providers that can be given an inbox.
//
// Receiving is the easier half: an inbox is a file the agent reads, and every
// provider can read a file. The asymmetry with liveSenders is real rather than
// an oversight — opencode hears about the others without being heard.
var liveReceivers = []string{"codex", "claude", "opencode", "antigravity"}

func isLiveSender(provider string) bool   { return containsProvider(liveSenders, provider) }
func isLiveReceiver(provider string) bool { return containsProvider(liveReceivers, provider) }

func containsProvider(list []string, provider string) bool {
	for _, candidate := range list {
		if candidate == provider {
			return true
		}
	}
	return false
}

// livePeerMatrix maps a receiving provider to the senders it accepts.
type livePeerMatrix map[string][]string

// sendersFor returns the providers whose work reaches this receiver.
//
// An absent receiver takes the default: every eligible provider but itself.
func (m livePeerMatrix) sendersFor(receiver string) []string {
	if configured, ok := m[receiver]; ok {
		return configured
	}
	var senders []string
	for _, candidate := range liveSenders {
		if candidate != receiver {
			senders = append(senders, candidate)
		}
	}
	return senders
}

// delivers reports whether sender's work should reach receiver.
func (m livePeerMatrix) delivers(sender, receiver string) bool {
	if sender == receiver || !isLiveSender(sender) || !isLiveReceiver(receiver) {
		return false
	}
	for _, candidate := range m.sendersFor(receiver) {
		if candidate == sender {
			return true
		}
	}
	return false
}

// receiversFor returns the providers that should be told about sender's work.
//
// This is the direction the running session needs: it knows what it did and
// has to decide whose inbox to write it to, including inboxes belonging to
// providers that are not running.
func (m livePeerMatrix) receiversFor(sender string) []string {
	var receivers []string
	for _, candidate := range liveReceivers {
		if m.delivers(sender, candidate) {
			receivers = append(receivers, candidate)
		}
	}
	sort.Strings(receivers)
	return receivers
}

// loadLivePeers reads the matrix, falling back to the default on any problem.
//
// A malformed line is skipped rather than failing the session: cross-agent
// delivery is an addition to a working session, and a typo in a config file is
// not a reason to refuse to start an agent. parseLivePeers reports what it
// skipped so the caller can say so.
func loadLivePeers(root string) (livePeerMatrix, []string) {
	data, err := os.ReadFile(livePeerPath(root))
	if err != nil {
		return livePeerMatrix{}, nil
	}
	return parseLivePeers(string(data))
}

func parseLivePeers(text string) (livePeerMatrix, []string) {
	matrix := livePeerMatrix{}
	var problems []string
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		receiver, list, found := strings.Cut(line, ":")
		receiver = strings.ToLower(strings.TrimSpace(receiver))
		if !found || receiver == "" {
			problems = append(problems, fmt.Sprintf("line %d: expected \"receiver: senders\"", i+1))
			continue
		}
		if !isLiveReceiver(receiver) {
			problems = append(problems, fmt.Sprintf("line %d: %q cannot receive live updates", i+1, receiver))
			continue
		}
		senders, bad := parseLiveSenders(receiver, list)
		for _, name := range bad {
			problems = append(problems, fmt.Sprintf("line %d: %q cannot send live updates", i+1, name))
		}
		// A line that named only senders that cannot send is a typo, not a
		// request for silence. Recording it as an empty list would turn the
		// mistake into a decision to stop delivering, and would quietly undo
		// whatever an earlier line said. Skip it instead.
		if len(senders) == 0 && len(bad) > 0 && !mentionsNone(list) {
			continue
		}
		matrix[receiver] = senders
	}
	return matrix, problems
}

// mentionsNone reports whether the operator asked for silence outright, as
// opposed to a list that happened to contain nothing usable.
func mentionsNone(list string) bool {
	for _, field := range splitSenderList(list) {
		if field == "none" {
			return true
		}
	}
	return false
}

func splitSenderList(list string) []string {
	return strings.FieldsFunc(strings.ToLower(list), func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
}

func parseLiveSenders(receiver, list string) ([]string, []string) {
	fields := splitSenderList(list)
	var senders, bad []string
	for _, name := range fields {
		switch {
		case name == "none":
			return nil, bad
		case name == "all":
			for _, candidate := range liveSenders {
				if candidate != receiver {
					senders = append(senders, candidate)
				}
			}
		case !isLiveSender(name) || name == receiver:
			bad = append(bad, name)
		default:
			senders = append(senders, name)
		}
	}
	sort.Strings(senders)
	return senders, bad
}

// saveLivePeers writes the matrix with every eligible receiver spelled out, so
// the file shows the whole decision rather than the part that differs from a
// default the reader has to know.
func saveLivePeers(root string, matrix livePeerMatrix) error {
	path := livePeerPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Which providers' work reaches which inbox, live.\n")
	b.WriteString("# One line per receiver: \"receiver: sender, sender\", or \"none\".\n\n")
	for _, receiver := range liveReceivers {
		senders := matrix.sendersFor(receiver)
		if len(senders) == 0 {
			fmt.Fprintf(&b, "%s: none\n", receiver)
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", receiver, strings.Join(senders, ", "))
	}
	return os.WriteFile(path, []byte(b.String()), 0600)
}
