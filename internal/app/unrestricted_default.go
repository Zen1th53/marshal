//go:build governed

package app

import "time"

// Community reads only folders an operator explicitly granted.
func (*Runtime) buildUndecidedRead(string) bool { return false }

// Community requires architect or appsec review for a developer's network on
// a high-risk task.
func buildHighRiskNetworkAllowed() bool { return false }

// Community bounds a task's fresh-session retries.
func buildMaxFreshSessionRetries() int { return 2 }

// Community bounds one governed task launch (one model turn) to 30 minutes.
func buildTaskLaunchTimeout() time.Duration { return 30 * time.Minute }

// Community's provider shell-exec grant lasts as long as its bounded launch.
func buildShellExecGrantTTL() time.Duration { return buildTaskLaunchTimeout() }
