# Concepts and known limits

## Governed and native sessions

A native session opens the installed provider's interface with your configuration
and its own permissions. A governed task follows MARSHAL's execution, isolation,
approval and verification controls. Capturing native conversation memory does
not make native work governed or verified.

## Plan pack

A plan pack is the written agreement: requirements, an index and task notes.
Review it before approving. Each task says what to produce and how to check it.
Approvals bind the current plan; material changes need a new decision.

## Operator boundary and approvals

You are the operator at the terminal. Agents cannot grant your approval.
Goal edits become pending revisions; decisions on stale revisions cannot authorize
continuation. `/approvals` shows typed IDs, `/approval inspect <id>` inspects an
item, and `/approve <id>` or `/reject <id> [reason]` records your decision.
Inspect the requested action and scope before approving.

Governed execution can fail closed when isolation or policy requirements are
unavailable. Imported records pass secret filtering and omit hidden reasoning.
These controls do not certify that every provider output is correct. Verify
changes and avoid entering secrets into prompts or shared records.

## Known limits in 0.0.5

- Navigation shortcuts are closed; all available commands remain in the composer.
- Alignment checks record scope and goal drift but are advisory and do not block tasks.
- Blind interpretation is unavailable and omitted from help.
- Harness selection does not apply an execution profile.
- OpenCode is a worker, not a Marshal planning model.
- Provider grammar qualification is limited to the versions in [provider setup](providers.md).
- Governed execution needing network access can be blocked by policy.
- Live database restore requires Linux; use offline restore on other systems.
- Keep the window open while a Marshal run is in progress.
- The release notes do not establish a real-model run from chat through accepted
  work. Test on a small project before relying on the complete workflow.
