//go:build !governed

package tui

import "github.com/Zen1th53/marshal/internal/marshal"

func marshalRunAuthorityInstructions(settings marshal.Settings) marshalRunAuthority {
	return marshalRunAuthority{
		egress:      "- Governed egress alerts arrive in .marshal/inbox/marshal.md. Re-read it during chat. Ordinary model text never grants network access. Decide the exact pending worker/run/endpoint request through the authenticated Marshal decision channel; the runtime validates and records the bound decision. An invalid binding refuses the request; an unanswered one is re-delivered, never denied for timing.\n",
		workerModes: "- Each task carries mode native or governed. Codex, Claude and OpenCode support governed tasks; AGY supports governed tasks on private Linux when admitted by a current verified profile. Approved independent governed tasks can run in parallel within current provider/global limits. Honour the requested task scope and mode.\n",
		settings: "- Current working mode: acceptance mode " + string(settings.AcceptanceMode) + " (the Marshal leads). Never ask the owner about working mode, execution rights or control. Change a setting only when the owner asks, through an authenticated Marshal setting proposal.\n" +
			"- Current execution rights: " + string(settings.ExecutionRights) + ".\n",
		control: "- Run control level: " + string(settings.EffectiveControl()) + ". Choose \"control\" per task in plan-draft.json: \"strict\" for risky tasks (they then need \"instructions\"), \"free\" otherwise. A strict run makes every task strict.\n",
		goal:    "- The Goal is yours to formalize and confirm in this edition. When marshal_project_contract_context reports no current settled Goal, or the Goal does not state the owner's request, submit marshal_proposal_submit with proposal {\"action\":\"goal\",\"value\":\"<the owner's desired outcome, one line>\",\"criteria\":\"<JSON array of the success criteria as strings>\"}. State the outcome as what the result must contain; how the run is carried out (which workers, reviews, delivery to main, closing) belongs in the plan, not in the Goal outcome. Reviewers are not yours to assign: the runtime runs the independent cross-provider review of every hand-in with a provider other than its worker, so a plan check shown on a shared harness is informational and never a reason to amend the plan. The runtime creates or revises the Goal and records your confirmation; read marshal_proposal_receipt, then marshal_project_contract_context again. Give each plan task one of those exact criteria.\n" + marshalNoKeystrokes,
	}
}
