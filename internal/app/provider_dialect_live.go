package app

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// learnGrammarFromHelp reads which commands the installed CLI offers from its
// own --help output, so a provider is understood at whatever version is
// installed instead of only at the one version a table was written for. A
// command the help lists is SUPPORTED with that help as evidence; one it
// does not list stays UNKNOWN (absence is not proof) and is still passed
// through. It runs at most once per installed binary; the dialect cache
// keeps the result until the binary changes.
func learnGrammarFromHelp(ctx context.Context, path string, d *ProviderDialect) {
	if d.QualifiedVersion != "" {
		return // the offline table already describes this exact version
	}
	top := providerHelp(ctx, path)
	if top == "" {
		return
	}
	evidence := d.Provider + " " + d.Version + " --help output (read at runtime)"
	listed := helpWords(top)
	for _, word := range strings.Split(providerCommandWords[d.Provider], ",") {
		if !listed[word] {
			continue
		}
		if _, known := d.operations[word]; !known {
			d.operations[word] = ProviderOperation{Status: ProviderSupported, Evidence: evidence}
		}
		verbs, ok := providerVerbWords[d.Provider][word]
		if !ok {
			continue
		}
		sub := helpWords(providerHelp(ctx, path, word))
		for _, verb := range strings.Split(verbs, ",") {
			op := word + " " + verb
			if _, known := d.operations[op]; !known && sub[verb] {
				d.operations[op] = ProviderOperation{Status: ProviderSupported, Evidence: d.Provider + " " + d.Version + " " + word + " --help output (read at runtime)"}
			}
		}
	}
}

func providerHelp(ctx context.Context, path string, args ...string) string {
	helpCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(helpCtx, path, append(args, "--help")...)
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Env = append(cmd.Environ(), "NO_COLOR=1", "TERM=dumb", "CI=1")
	output := &providerProbeOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	_ = cmd.Run() // some CLIs exit non-zero after printing help
	if helpCtx.Err() != nil {
		return ""
	}
	return string(output.data)
}

// helpCommand matches a command at the start of a help line, the way every
// provider CLI lays out its command list ("  mcp   Manage MCP servers").
var helpCommand = regexp.MustCompile(`(?m)^\s{1,8}([a-z][a-z0-9-]*)(?:[ ,|]|$)`)

func helpWords(help string) map[string]bool {
	words := map[string]bool{}
	for _, m := range helpCommand.FindAllStringSubmatch(help, -1) {
		words[m[1]] = true
	}
	return words
}
