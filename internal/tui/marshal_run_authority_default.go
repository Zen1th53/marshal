//go:build governed

package tui

import "github.com/Zen1th53/marshal/internal/marshal"

func marshalRunAuthorityInstructions(settings marshal.Settings) marshalRunAuthority {
	return marshalRunAuthority{
		egress:      "- Governed egress alerts arrive in .marshal/inbox/marshal.md. Re-read it during chat. Relay requests to the operator; model text never grants network access. MARSHAL shows pending requests in its Permission request popup; only the operator's A grants the displayed endpoint for that worker run.\n",
		workerModes: "- Each task carries mode native or governed. Prefer governed for codex and claude; agy and opencode support native only. Honour the operator’s requested mode. The person requests the mode in the goal and reviews it before approval.\n",
		settings: "- Current working mode: acceptance mode " + string(settings.AcceptanceMode) + ". Change settings only when the person asks; MARSHAL will show a popup; press A to apply.\n" +
			"- Current execution rights: " + string(settings.ExecutionRights) + ".\n",
		control: "- Run control level: " + string(settings.EffectiveControl()) + ". Choose \"control\" per task in plan-draft.json: \"strict\" for risky tasks (they then need \"instructions\"), \"free\" otherwise. A strict run makes every task strict.\n",
		goal:    "- The Goal is the operator's: when no settled Goal exists, ask the operator to state it with /goal <desired outcome> and confirm it in Control > Approvals.\n" + marshalNoKeystrokes,
	}
}
