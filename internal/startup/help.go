package startup

import (
	"fmt"
	"sort"
	"strings"
)

// Help explains startup itself: what the states mean, why something is
// blocked, and what Setup and Doctor each do.
//
// It is available in every phase short of a core failure, because the moment a
// user most needs an explanation of why MARSHAL will not run their work is
// exactly the moment a health check has failed.

// HelpTopic is one explanation.
type HelpTopic struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// helpTopics is the fixed set. It is written in the second person and avoids
// internal vocabulary: a user reading it should not have to know what a lease,
// a reconciliation pass or a capability grant is.
var helpTopics = []HelpTopic{
	{
		Key:   "startup",
		Title: "What happens when MARSHAL starts",
		Body: "MARSHAL opens its control center first and checks your environment second. " +
			"If something is missing, the control center still opens and tells you what it found. " +
			"You can always reach Setup, Doctor, Help and your history, even when work cannot run.",
	},
	{
		Key:   "setup-vs-doctor",
		Title: "Setup and Doctor",
		Body: "Setup looks at your environment and reports what is ready. It never changes anything. " +
			"Doctor explains what is wrong, why it matters and what would fix it. " +
			"If Doctor can fix something itself, it asks you first, then re-checks and tells you whether the problem is actually gone. " +
			"MARSHAL will not install tools, create a repository or change your settings on its own.",
	},
	{
		Key:   "health",
		Title: "What the health states mean",
		Body: "Ready means checked and working. " +
			"Limited means working, with something unavailable that is named. " +
			"Needs attention means present but not working properly. " +
			"Missing means absent. Optional means absent and not needed for what you are doing. " +
			"Unknown means it could not be checked — which is never treated as working.",
	},
	{
		Key:   "blocked",
		Title: "Why work can be blocked",
		Body: "Work is blocked when running it would not be safe or would not be reproducible: " +
			"no project, no Git, no sandbox to isolate changes, or no policy saying what is permitted. " +
			"Blocking work does not block MARSHAL. You keep Setup, Doctor, Help, your project history and recovery.",
	},
	{
		Key:   "projects",
		Title: "Projects",
		Body: "A project is a Git repository that MARSHAL has been set up in. " +
			"Starting MARSHAL in a directory that is not a repository is normal — you will be offered the choice to " +
			"initialize one, open another, or pick from recent projects. Nothing is created until you ask for it.",
	},
	{
		Key:   "recovery",
		Title: "Interrupted work",
		Body: "If a previous run ended unexpectedly, MARSHAL tidies up what was left behind and tells you what it found. " +
			"It does not resume anything on its own. You choose whether to resume, inspect or discard the earlier work.",
	},
	{
		Key:   "modes",
		Title: "Standard and ULTRA",
		Body: "Standard is the default and is fully functional. " +
			"ULTRA adds depth and parallelism and requires a valid entitlement. " +
			"It is never switched on automatically, and it never removes a safety requirement that applies in Standard.",
	},
	{
		Key:   "providers",
		Title: "Provider readiness",
		Body: "MARSHAL reports four separate things about a provider: whether the tool is installed, " +
			"whether a credential is available, whether the tool can reach its service on its own, and whether MARSHAL " +
			"itself can run work through it under policy. Only the last one means the provider is usable here, " +
			"and MARSHAL will not claim it without having done it.",
	},
	{
		Key:   "privacy",
		Title: "What MARSHAL records",
		Body: "Startup records what it checked and what it found, so problems can be diagnosed later. " +
			"It does not record the contents of your files, your prompts, or your credentials.",
	},
}

// HelpTopics returns every topic in a stable order.
func HelpTopics() []HelpTopic {
	out := make([]HelpTopic, len(helpTopics))
	copy(out, helpTopics)
	sort.SliceStable(out, func(a, b int) bool { return out[a].Key < out[b].Key })
	return out
}

// Help returns one topic by key.
func Help(key string) (HelpTopic, bool) {
	for _, topic := range helpTopics {
		if topic.Key == key {
			return topic, true
		}
	}
	return HelpTopic{}, false
}

// ExplainBlocked answers "why can't I run anything?" using the assessment's own
// blocking checks, so the explanation names the real cause rather than a
// generic message.
func ExplainBlocked(assessment Assessment) string {
	blocking := assessment.Blocking()
	if len(blocking) == 0 {
		if assessment.ExecutionPermitted() {
			return "Nothing is blocking work."
		}
		return "Work is not available yet. Check the items listed as needing attention."
	}
	var b strings.Builder
	b.WriteString("Work cannot run because:\n")
	for _, check := range blocking {
		fmt.Fprintf(&b, "  - %s\n", check.Summary)
		if check.Impact != "" {
			fmt.Fprintf(&b, "    %s\n", check.Impact)
		}
		if check.Remedy != "" {
			fmt.Fprintf(&b, "    What would help: %s\n", check.Remedy)
		}
	}
	b.WriteString("\nSetup, Doctor, Help, project history and recovery remain available.")
	return b.String()
}
