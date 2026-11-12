//go:build !governed

package app

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
	"github.com/Zen1th53/marshal/internal/architecturereview"
	"github.com/Zen1th53/marshal/internal/nativehistory"
	"github.com/Zen1th53/marshal/internal/usage"
)

// architectureBuilders lists the providers the owner may pick per part,
// with each one's last reported limit. Shipped build only.
func (r *Runtime) architectureBuilders() func(context.Context) []architecturereview.BuilderOption {
	return func(ctx context.Context) []architecturereview.BuilderOption {
		glyphs := map[string]string{"claude": "✳", "codex": "⬡", "agy": "◆", "opencode": "●"}
		codexDir, err := nativehistory.HistoryDir(nativehistory.Codex, r.ProjectRoot())
		if err != nil {
			codexDir = ""
		}
		quotas := usage.Read(time.Now(), usage.Sources{CodexDir: codexDir, ClaudeFile: usage.ClaudeConfigFile()})
		options := make([]architecturereview.BuilderOption, 0, len(architecturecodec.BuilderProviders))
		for _, provider := range architecturecodec.BuilderProviders {
			option := architecturereview.BuilderOption{ID: provider, Glyph: glyphs[provider]}
			if provider == "agy" && !privateAGYEnabled() {
				option.Reason = "not available in this build"
			} else if _, lookErr := exec.LookPath(provider); lookErr != nil {
				option.Reason = "not installed"
			} else if _, limited := r.providerLimited(provider); limited {
				option.Reason = "at its limit right now"
			} else if r.workerIneligibilityReason(ctx, provider) != "" {
				option.Reason = "not checked on this machine yet"
			} else {
				option.Available = true
				option.Limit = builderLimitText(quotas, provider)
			}
			options = append(options, option)
		}
		return options
	}
}

// builderLimitText renders a provider's last reported quota in plain English.
// An unreadable or unexposed limit is "limit unknown"; never estimated. The
// percent left is rounded down, as the launch picker and vitals row show it.
func builderLimitText(quotas []usage.Quota, provider string) string {
	for _, quota := range quotas {
		if quota.Provider != provider {
			continue
		}
		if !quota.Known {
			return "limit unknown"
		}
		var windows []string
		reset := ""
		for _, window := range quota.Windows {
			if !window.Known() {
				continue
			}
			windows = append(windows, fmt.Sprintf("%s %d%% left", window.Label, int(math.Floor(window.LeftPercent))))
			if reset == "" {
				reset = window.ResetsAt.Local().Format("15:04")
			}
		}
		if len(windows) == 0 {
			return "limit unknown"
		}
		text := strings.Join(windows, ", ")
		if reset != "" {
			text += " · resets " + reset
		}
		return text
	}
	return "limit unknown"
}
