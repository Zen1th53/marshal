package cli

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/opencodecli"
)

// adapterObservation is what `marshal adapters` reports for one provider
// CLI. It runs only `--version` (and, for OpenCode, `run --help`); neither
// opens a model session.
type adapterObservation struct {
	Available bool
	Version   string
	Pinned    string
	Detail    string
}

var observeAdapterCLI = func(ctx context.Context, name, binary string) adapterObservation {
	if name == "opencode" {
		return observeOpenCode(ctx, binary, app.PinnedOpenCodeVersion())
	}
	raw, err := adapterVersionOutput(ctx, binary)
	if err != nil {
		return adapterObservation{Detail: "version probe failed: " + err.Error()}
	}
	return adapterObservation{Available: true, Version: firstLine(raw)}
}

func observeOpenCode(ctx context.Context, binary, pinned string) adapterObservation {
	c, err := opencodecli.ProbeContract(ctx, binary)
	if err != nil {
		return adapterObservation{Pinned: pinned, Detail: err.Error()}
	}
	return judgeOpenCode(c, pinned)
}

// judgeOpenCode accepts a CLI whose observed run flags and JSON stream match
// what MARSHAL uses. A version other than the pinned bootstrap is named, not
// hidden; it is accepted only with a verified contract.
func judgeOpenCode(c opencodecli.Contract, pinned string) adapterObservation {
	obs := adapterObservation{Version: c.Version, Pinned: pinned}
	if obs.Version == "" {
		obs.Version = c.RawVersion
	}
	if err := c.WorkerError(); err != nil {
		obs.Detail = err.Error()
		if pinned != "" && c.Version != pinned {
			obs.Detail = fmt.Sprintf("installed %s differs from pinned %s and %s", obs.Version, pinned, err.Error())
		}
		return obs
	}
	obs.Available = true
	if pinned != "" && c.Version != pinned {
		obs.Detail = fmt.Sprintf("installed %s differs from pinned %s; run contract verified", c.Version, pinned)
	} else {
		obs.Detail = "run contract verified"
	}
	if c.Standalone {
		obs.Detail += "; launches use --standalone"
	}
	return obs
}

func adapterVersionOutput(ctx context.Context, binary string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, binary, "--version")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}
