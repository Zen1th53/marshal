package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

// Every sender/receiver pair, exercised end to end.
//
// The pairs that cannot work are as interesting as the ones that can, and both
// are asserted here rather than described in a comment somewhere: a provider
// whose history is a database it holds open cannot be read mid-session, and no
// amount of configuration changes that. The test fails if a future change
// quietly makes one of those pairs appear to deliver, because that would mean
// delivering something MARSHAL cannot actually observe.
func TestLiveDeliveryAcrossEveryProviderPair(t *testing.T) {
	providers := []string{"claude", "codex", "opencode", "antigravity"}

	type outcome struct {
		sender, receiver string
		delivered        bool
		reason           string
	}
	var results []outcome

	for _, sender := range providers {
		for _, receiver := range providers {
			root := t.TempDir()
			matrix, problems := loadLivePeers(root)
			if len(problems) != 0 {
				t.Fatalf("default matrix reported problems: %v", problems)
			}

			want := matrix.delivers(sender, receiver)

			// Drive the real path: open the receiver's inbox the way a running
			// sender does, append a message, and read the file back.
			box, err := openPeerInbox(root, receiver)
			if err != nil {
				t.Fatalf("%s→%s open inbox: %v", sender, receiver, err)
			}
			if want {
				if err := box.append(sender, importer.Message{
					Role:      "assistant",
					Content:   fmt.Sprintf("%s edited internal/store/sqlite.go", sender),
					Timestamp: time.Date(2026, 9, 22, 7, 0, 0, 0, time.UTC),
				}); err != nil {
					t.Fatalf("%s→%s append: %v", sender, receiver, err)
				}
			}
			data, err := os.ReadFile(inboxPath(root, receiver))
			if err != nil {
				t.Fatalf("%s→%s read inbox: %v", sender, receiver, err)
			}
			got := strings.Contains(string(data), sender+" edited internal/store/sqlite.go")
			if got != want {
				t.Errorf("%s→%s delivered=%v, matrix says %v", sender, receiver, got, want)
			}

			results = append(results, outcome{sender, receiver, want, deliveryReason(sender, receiver, want)})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].sender != results[j].sender {
			return results[i].sender < results[j].sender
		}
		return results[i].receiver < results[j].receiver
	})
	var b strings.Builder
	b.WriteString("\nLIVE DELIVERY MATRIX (default configuration)\n")
	fmt.Fprintf(&b, "%-14s %-14s %-9s %s\n", "SENDER", "RECEIVER", "DELIVERS", "WHY")
	for _, r := range results {
		mark := "no"
		if r.delivered {
			mark = "yes"
		}
		fmt.Fprintf(&b, "%-14s %-14s %-9s %s\n", r.sender, r.receiver, mark, r.reason)
	}
	t.Log(b.String())
}

func deliveryReason(sender, receiver string, delivered bool) string {
	switch {
	case sender == receiver:
		return "same provider; an agent is not told its own work"
	case !isLiveSender(sender):
		return "history is a database the provider holds open; imported on exit"
	case !isLiveReceiver(receiver):
		return "no inbox is written for this provider"
	case delivered:
		return "append-only history, read while it runs"
	default:
		return "disabled by configuration"
	}
}

// Turning a pair off and on again must take effect, and must not disturb the
// other direction or the other pairs.
func TestLiveDeliveryHonoursConfigurationPerPair(t *testing.T) {
	root := t.TempDir()
	if err := saveLivePeers(root, livePeerMatrix{"opencode": nil}); err != nil {
		t.Fatalf("save: %v", err)
	}
	matrix, problems := loadLivePeers(root)
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}

	for _, sender := range []string{"claude", "codex"} {
		if matrix.delivers(sender, "opencode") {
			t.Errorf("%s still reaches opencode after it was set to none", sender)
		}
		for _, receiver := range []string{"claude", "codex", "antigravity"} {
			if receiver == sender {
				continue
			}
			if !matrix.delivers(sender, receiver) {
				t.Errorf("%s→%s stopped delivering, which was never asked for", sender, receiver)
			}
		}
	}
}

// A provider that cannot send must stay unable to send however the operator
// writes the configuration. The limit is what MARSHAL can observe, not a
// preference.
func TestLiveDeliveryRefusesToEnableAnUnreadableSender(t *testing.T) {
	for _, sender := range []string{"opencode", "antigravity"} {
		matrix, problems := parseLivePeers(fmt.Sprintf("claude: %s\n", sender))
		if len(problems) != 1 || !strings.Contains(problems[0], "cannot send") {
			t.Errorf("configuring %s as a sender reported %v", sender, problems)
		}
		if matrix.delivers(sender, "claude") {
			t.Errorf("%s was enabled as a live sender", sender)
		}
	}
}
