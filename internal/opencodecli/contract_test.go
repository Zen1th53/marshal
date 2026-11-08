package opencodecli

import (
	"slices"
	"strings"
	"testing"
)

// Observed verbatim from the installed /usr/bin/opencode 2.1.4 on 2026-10-29.
const runHelp214 = `DESCRIPTION
  Run OpenCode with a message

USAGE
  opencode run [flags] [<message...>]

ARGUMENTS
  message... string    Message to send (optional)

FLAGS
  --standalone            Run with a private server instead of the background service
  --server string         Connect to a server URL instead of the background service
  --continue, -c          Continue the last session
  --session, -s string    Session ID to continue, or to create if it does not exist
  --fork                  Fork the session before continuing
  --model, -m string      Model to use in the format provider/model#variant
  --agent string          Agent to use
  --format choice         Output format (choices: default, json)
  --file, -f string       File to attach to the message
  --title string          Session title
  --thinking              Show thinking blocks
  --auto                  Auto-approve permissions that are not explicitly denied
`

const runHelp1x = `opencode run [message..]

run opencode with a message

Options:
      --format    format: default (formatted) or json (raw JSON events)  [string] [choices: "default", "json"] [default: "default"]
  -m, --model     model to use in the format of provider/model             [string]
  -s, --session   session id to continue                                   [string]
      --dir       directory to run in, path on remote server if attaching  [string]
`

func TestNormalizeVersionAcceptsBothCLIGenerations(t *testing.T) {
	for raw, want := range map[string]string{"opencode v2.1.4\n": "2.1.4", "1.18.16": "1.18.16", "garbage": ""} {
		if got := NormalizeVersion(raw); got != want {
			t.Fatalf("%q: got %q want %q", raw, got, want)
		}
	}
}

func TestInstalledTwoXContractMatchesWorkerUse(t *testing.T) {
	c := ParseRunHelp(runHelp214)
	c.RawVersion, c.Version = "opencode v2.1.4", "2.1.4"
	if err := c.WorkerError(); err != nil {
		t.Fatal(err)
	}
	if !c.Standalone || c.Dir {
		t.Fatalf("2.x flags misread: %+v", c)
	}
	got := c.RunArgs([]string{"run", "--format", "json", "--dir", "/wt", "-m", "m", "brief"})
	if !slices.Equal(got, []string{"run", "--standalone", "--format", "json", "-m", "m", "brief"}) {
		t.Fatalf("2.x argv: %v", got)
	}
	if got := c.RunArgs([]string{"--session", "s"}); !slices.Equal(got, []string{"--standalone", "--session", "s"}) {
		t.Fatalf("interactive argv: %v", got)
	}
	if got := c.RunArgs([]string{"run", "--server", "http://x", "b"}); slices.Contains(got, "--standalone") {
		t.Fatalf("explicit server overridden: %v", got)
	}
}

func TestOneXContractKeepsArgv(t *testing.T) {
	c := ParseRunHelp(runHelp1x)
	c.RawVersion, c.Version = "1.18.16", "1.18.16"
	if err := c.WorkerError(); err != nil {
		t.Fatal(err)
	}
	in := []string{"run", "--format", "json", "--dir", "/wt", "brief"}
	if got := c.RunArgs(in); !slices.Equal(got, in) {
		t.Fatalf("1.x argv changed: %v", got)
	}
}

func TestTwoXWithoutStandaloneIsRejected(t *testing.T) {
	c := ParseRunHelp(strings.ReplaceAll(runHelp214, "--standalone", "--other"))
	c.RawVersion, c.Version = "opencode v2.9.0", "2.9.0"
	if err := c.WorkerError(); err == nil || !strings.Contains(err.Error(), "--standalone") {
		t.Fatalf("missing standalone accepted: %v", err)
	}
	c = ParseRunHelp("USAGE\n  opencode run\n")
	c.Version = "3.0.0"
	if err := c.WorkerError(); err == nil || !strings.Contains(err.Error(), "--format json") {
		t.Fatalf("missing json accepted: %v", err)
	}
}
