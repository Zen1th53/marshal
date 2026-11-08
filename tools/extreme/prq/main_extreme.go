//go:build !governed

package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func main() {
	var (
		statusFile        = flag.String("status-file", ".marshal/prq-status.jsonl", "path to JSON status lines file")
		mainBranch        = flag.String("main", "main", "main branch name")
		remote            = flag.String("remote", "origin", "remote name")
		pollInterval      = flag.Int("poll-interval-ms", 5000, "poll interval in ms")
		maxPollAttempts   = flag.Int("max-poll-attempts", 720, "max poll attempts")
		initialBackoffMs  = flag.Int("initial-backoff-ms", 1000, "initial backoff in ms")
		maxBackoffMs      = flag.Int("max-backoff-ms", 30000, "max backoff in ms")
		maxNetworkRetries = flag.Int("max-network-retries", 5, "max consecutive network retries")
		singlePR          = flag.Int("pr", 0, "single PR number")
		singleWorktree    = flag.String("worktree", "", "worktree path for single PR")
	)
	flag.Parse()

	var prs []PRItem
	if *singlePR > 0 && *singleWorktree != "" {
		prs = append(prs, PRItem{Number: *singlePR, Worktree: *singleWorktree})
	}

	args := flag.Args()
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.Contains(arg, ":") {
			parts := strings.SplitN(arg, ":", 2)
			num, err := strconv.Atoi(parts[0])
			if err == nil && parts[1] != "" {
				prs = append(prs, PRItem{Number: num, Worktree: parts[1]})
			}
			continue
		}
		if i+1 < len(args) {
			num, err := strconv.Atoi(arg)
			if err == nil {
				prs = append(prs, PRItem{Number: num, Worktree: args[i+1]})
				i++
				continue
			}
		}
	}

	if len(prs) == 0 {
		fmt.Fprintln(os.Stderr, "usage: prq [flags] <pr-num> <worktree> ... or <pr-num>:<worktree> ...")
		os.Exit(1)
	}

	cfg := Config{
		MainBranch:        *mainBranch,
		Remote:            *remote,
		StatusFile:        *statusFile,
		PollInterval:      *pollInterval,
		MaxPollAttempts:   *maxPollAttempts,
		InitialBackoffMs:  *initialBackoffMs,
		MaxBackoffMs:      *maxBackoffMs,
		MaxNetworkRetries: *maxNetworkRetries,
	}

	if err := RunQueue(cfg, prs); err != nil {
		fmt.Fprintf(os.Stderr, "prq error: %v\n", err)
		os.Exit(1)
	}
}
