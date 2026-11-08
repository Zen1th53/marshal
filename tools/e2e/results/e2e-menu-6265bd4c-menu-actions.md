# Advanced menu (Ctrl+N) action matrix, tui/e2e-menu (6265bd4c, menu fixes of the 2026-11-06 e2e), default build, 2026-11-06

Result: tui_walk.py over all 117 Ctrl+N actions on an isolated tmux server (own TMUX_TMPDIR, HOME and project clone, PATH with no provider binaries, no Marshal chat, no provider quota), menu pane 400 columns, --wired internal/tui/wired.json. 0 ERROR/DEAD/UNREACHABLE/CHANGED. Against the 9592f7f6 walk, CTUI-0079/0080/0248/0317/0319 moved from FORM to DISABLED: they now refuse before the form opens when their precondition (a goal, a team session) is missing. The deep probe of every action is in e2e-menu-20261106.md.


States: FORM = typed form opened; CONFIRM = confirmation opened; RESULT = confirmation resolved immediately to a verdict (usually BLOCKED by state); DISABLED = refused with a reason; HIDDEN = intentionally not offered (this edition, or under construction: hidden until wired, cause kept); ERROR/DEAD/UNREACHABLE/CHANGED = broken.

Totals: CONFIRM 12, DISABLED 12, FORM 51, HIDDEN 16, RESULT 26


## Control

| ID | Type | Path | State | Detail |
|---|---|---|---|---|
| CTUI-0031 | ACTION | Control / Mode & Autonomy / Manual Standard mode | CONFIRM |  |
| CTUI-0032 | ACTION | Control / Mode & Autonomy / Automatic Standard mode | CONFIRM |  |
| CTUI-0033 | GOVERNED | Control / Mode & Autonomy / ULTRA mode | CONFIRM |  |
| CTUI-0034 | ACTION | Control / Mode & Autonomy / ULTRA execution preference | CONFIRM |  |
| CTUI-0035 | GOVERNED | Control / Mode & Autonomy / Request ULTRA entitlement | HIDDEN | not offered in Extreme: ULTRA is local |
| CTUI-0038 | GOVERNED | Control / Execution / Start approved plan run | RESULT | BLOCKED: the current plan could not be read: execution plan not found: project PROJECT-a6ccafa04a2267214fdd120d7c5146af has no plan |
| CTUI-0039 | GOVERNED | Control / Execution / Execute or continue run | RESULT | BLOCKED: no run is active in this session; start an approved plan run first |
| CTUI-0040 | GOVERNED | Control / Execution / Run task through binary-backed adapter | RESULT | BLOCKED: no run is active in this session; start an approved plan run first |
| CTUI-0041 | GOVERNED | Control / Execution / Claim or assign task | RESULT | BLOCKED: the canonical ready-task queue is empty |
| CTUI-0042 | ACTION | Control / Execution / Release task lease | RESULT | BLOCKED: no leased canonical task is available |
| CTUI-0043 | ACTION | Control / Execution / Pause task | FORM | ▸ Exact task ID *: (empty) |
| CTUI-0044 | ACTION | Control / Execution / Resume task | FORM | ▸ Exact task ID *: (empty) |
| CTUI-0045 | ACTION | Control / Execution / Retry task | FORM | ▸ Exact task ID *: (empty) |
| CTUI-0046 | DESTRUCTIVE | Control / Execution / Cancel task | DISABLED | cannot start cancel task: no non-terminal canonical task is available |
| CTUI-0047 | DESTRUCTIVE | Control / Execution / Cancel plan | RESULT | BLOCKED: the current plan could not be read: execution plan not found: project PROJECT-a6ccafa04a2267214fdd120d7c5146af has no plan |
| CTUI-0048 | DESTRUCTIVE | Control / Execution / Cancel goal | DISABLED | cannot start cancel goal: the active goal could not be read: there is no goal yet; create and confirm one first |
| CTUI-0050 | GOVERNED | Control / Execution / Execution Cells / Prepare governed cell | FORM | ▸ Canonical task ID with its existing approved-plan worktree *: (empty); Approved check index (0 is the first check) *: (empty) |
| CTUI-0051 | GOVERNED | Control / Execution / Execution Cells / Execute governed tool/action | FORM | ▸ Exact cell ID (one-shot descriptor returned by Prepare, or a run-owned execution cell) *: (empty) |
| CTUI-0052 | DESTRUCTIVE | Control / Execution / Execution Cells / Destroy cell | FORM | ▸ Exact cell ID (one-shot descriptor returned by Prepare, or a run-owned execution cell) *: (empty) |
| CTUI-0060 | GOVERNED | Control / Approvals / Approve | RESULT | BLOCKED: no approval is awaiting a decision |
| CTUI-0061 | GOVERNED | Control / Approvals / Reject | RESULT | BLOCKED: no approval is awaiting a decision |
| CTUI-0065 | ACTION | Control / Checkpoints / Create execution checkpoint | RESULT | BLOCKED: no run is active in this session; start an approved plan run first |
| CTUI-0070 | DESTRUCTIVE | Control / Rollback & Recovery / Roll back execution checkpoint | RESULT | BLOCKED: no durable checkpoint exists to roll back to |
| CTUI-0071 | DESTRUCTIVE | Control / Rollback & Recovery / Roll back memory state | FORM | ▸ Exact durable memory snapshot ID *: (empty); Restore rationale *: (empty) |
| CTUI-0072 | GOVERNED | Control / Rollback & Recovery / Roll back optimization canary | RESULT | BLOCKED: no pending or running optimization canary is available |
| CTUI-0073 | ACTION | Control / Rollback & Recovery / Resume interrupted session | FORM | ▸ Exact durable workspace recovery ID, or exact paused canonical run ID of this session (native conversations use /resume) *: (empty) |
| CTUI-0074 | GOVERNED | Control / Rollback & Recovery / Resume from valid checkpoint | RESULT | BLOCKED: no durable checkpoint exists to roll back to |
| CTUI-0075 | ACTION | Control / Rollback & Recovery / Apply bounded recovery retry | RESULT | BLOCKED: no run is active in this session; start an approved plan run first |
| CTUI-0077 | GOVERNED | Control / Budgets / Token limit | DISABLED | cannot start token limit: the active goal could not be read: there is no goal yet; create and confirm one first |
| CTUI-0078 | GOVERNED | Control / Budgets / Cost limit | DISABLED | cannot start cost limit: the active goal could not be read: there is no goal yet; create and confirm one first |
| CTUI-0079 | GOVERNED | Control / Budgets / Time limit | DISABLED | cannot start time limit: the active goal could not be read: there is no goal yet; create and confirm one first |
| CTUI-0080 | GOVERNED | Control / Budgets / Model-call limit | DISABLED | cannot start model-call limit: the active goal could not be read: there is no goal yet; create and confirm one first |
| CTUI-0081 | GOVERNED | Control / Budgets / Handoff limit | DISABLED | cannot start handoff limit: the active goal could not be read: there is no goal yet; create and confirm one first |
| CTUI-0082 | GOVERNED | Control / Budgets / Verification reserve | DISABLED | cannot start verification reserve (model calls): the active goal could not be read: there is no goal yet; create and confirm one first |

## Work

| ID | Type | Path | State | Detail |
|---|---|---|---|---|
| CTUI-0201 | ACTION | Work / Projects & Setup / Initialize MARSHAL / Create local state | RESULT | BLOCKED: already initialized: .marshal-extreme present #e3b0c44298fc; initialization runs once through `marshal init` |
| CTUI-0202 | ACTION | Work / Projects & Setup / Initialize MARSHAL / Install default capability policy | RESULT | BLOCKED: already initialized: CAPABILITIES.yaml present #b3cb698c174f; initialization runs once through `marshal init` |
| CTUI-0203 | ACTION | Work / Projects & Setup / Initialize MARSHAL / Install pack/runtime contracts | RESULT | BLOCKED: already initialized: PACK-VERSION.yaml, RUNTIME-VERSION.yaml present #0e11853b83b8; initialization runs once through `marshal init` |
| CTUI-0210 | GOVERNED | Work / Projects & Setup / Readiness & Doctor / Consented initialization repair | HIDDEN | owner decision: startup.Repair's only repairable condition (STARTUP_PROJECT_NOT_INITIALIZED) cannot exist while a workspace has the project open, and its fix (app.Bootstrap / marshal setup) must not run against the store the workspace holds. A binding here could only ever refuse. Either widen startup.repairableReasons (Process 01 forbids silent repair) or keep repair in `marshal setup` / `marshal doctor` before launch (P10 review 2026-11-05) |
| CTUI-0219 | ACTION | Work / Projects & Setup / Sessions / Open by session ID | FORM | ▸ Session ID to open (the workspace relaunches as `marshal tui SESSION-ID` in this terminal) *: (empty) |
| CTUI-0229 | ACTION | Work / Goal & Intent — Process 03 / Form goal from request | FORM | ▸ Request (your words) *: (empty) |
| CTUI-0239 | GOVERNED | Work / Goal & Intent — Process 03 / Persist and activate confirmed goal | RESULT | BLOCKED: the active goal could not be read: there is no goal yet; create and confirm one first |
| CTUI-0240 | GOVERNED | Work / Goal & Intent — Process 03 / Revise as new goal revision | DISABLED | cannot start revise as new goal revision: the active goal could not be read: there is no goal yet; create and confirm one first |
| CTUI-0248 | ACTION | Work / Blind Interpretation / Resolve with user correction | DISABLED | cannot start resolve with user correction: the active goal could not be read: there is no goal yet; create and confirm one first |
| CTUI-0249 | GOVERNED | Work / Blind Interpretation / Invalidate affected claims | FORM | ▸ Exact verification session ID *: (empty); Claim ID *: (empty); Canonical counter-evidence ID *: (empty); Challenge reason *: (empty) |
| CTUI-0251 | ACTION | Work / Plan — Process 04 / Create plan | RESULT | BLOCKED: the active goal could not be read: there is no goal yet; create and confirm one first |
| CTUI-0259 | GOVERNED | Work / Plan — Process 04 / Hand off approved plan to Process 05 | RESULT | BLOCKED: the current plan could not be read: execution plan not found: project PROJECT-a6ccafa04a2267214fdd120d7c5146af has no plan |
| CTUI-0266 | GOVERNED | Work / Task Graph / Authorized dynamic DAG changes | HIDDEN | Runtime.DAG() is a dag.Engine built with NewEngine (no IdentityProvider, Authorizer or FreshnessValidator), so it cannot perform the 'authorized CAS graph mutation' the spec names, and no production code reads dag_nodes: execution service schedules from plan tasks. An edge added here would be unauthorized and change nothing. Needs the authorized engine wired into execution first (P10 review 2026-11-05) |
| CTUI-0295 | ACTION | Work / Tasks / Import tasks | RESULT | BLOCKED: the current plan could not be read: execution plan not found: project PROJECT-a6ccafa04a2267214fdd120d7c5146af has no plan |
| CTUI-0297 | ACTION | Work / Tasks / Create task | RESULT | BLOCKED: the current plan could not be read: execution plan not found: project PROJECT-a6ccafa04a2267214fdd120d7c5146af has no plan |
| CTUI-0313 | ACTION | Work / Collaboration & Handoffs / Register agent | CONFIRM |  |
| CTUI-0317 | GOVERNED | Work / Collaboration & Handoffs / Messages | DISABLED | cannot start messages: no collaboration session default-session exists for this workspace; start a team session first |
| CTUI-0318 | GOVERNED | Work / Collaboration & Handoffs / Claim challenges and counter-evidence | FORM | ▸ Claim ID *: (empty); Stored counter-evidence ID *: (empty); Why the evidence contradicts the claim *: (empty) |
| CTUI-0319 | GOVERNED | Work / Collaboration & Handoffs / Ownership handoff | DISABLED | cannot start ownership handoff: no collaboration session default-session exists for this workspace; start a team session first |
| CTUI-0320 | GOVERNED | Work / Collaboration & Handoffs / Typed task handoff | FORM | ▸ Task ID *: (empty); Target role (developer, qa, architect, appsec, orchestrator) *: (empty); Handoff summary *: (empty) |
| CTUI-0321 | GOVERNED | Work / Collaboration & Handoffs / Memory handoff | FORM | ▸ Task ID whose memory is handed over *: (empty); Target role (developer, qa, architect, appsec, orchestrator) *: (empty) |
| CTUI-0329 | DESTRUCTIVE | Work / Workspaces & Worktrees / Garbage collect eligible worktrees | CONFIRM |  |

## Verify

| ID | Type | Path | State | Detail |
|---|---|---|---|---|
| CTUI-0338 | ACTION | Verify / Review — Process 06 / Start verification session | RESULT | BLOCKED: no run is active in this session; start an approved plan run first |
| CTUI-0342 | GOVERNED | Verify / Review — Process 06 / Evaluate verification | RESULT | BLOCKED: no run is active, so no verification applies |
| CTUI-0352 | GOVERNED | Verify / Claims / Challenge with counter-evidence | FORM | ▸ Exact verification session ID *: (empty); Claim ID *: (empty); Canonical counter-evidence ID *: (empty); Challenge reason *: (empty) |
| CTUI-0365 | GOVERNED | Verify / Verification Methods / Run governed local verification command | RESULT | BLOCKED: unavailable: Codex CLI is unavailable |
| CTUI-0382 | GOVERNED | Verify / Findings & Contradictions / Authorized finding closure | FORM | ▸ Exact verification session ID *: (empty); Exact finding ID *: (empty); Closure reason *: (empty); Waiver expiry (RFC3339, within 24h) *: (empty) |
| CTUI-0395 | GOVERNED | Verify / Completion / Completion attestation | FORM | ▸ Exact verification session ID *: (empty) |
| CTUI-0399 | ACTION | Verify / Evidence Bundles / Build bundle | FORM | ▸ Exact verification session ID *: (empty) |
| CTUI-0402 | ACTION | Verify / Evidence Bundles / Export bundle | FORM | ▸ Exact verification session ID *: (empty); New project-relative export file *: (empty) |

## Memory

| ID | Type | Path | State | Detail |
|---|---|---|---|---|
| CTUI-0424 | ACTION | Memory / Capture & Remember / Remember semantic knowledge | FORM | ▸ Memory title *: (empty); Knowledge *: (empty) |
| CTUI-0425 | ACTION | Memory / Capture & Remember / Extract candidate from run or handoff | FORM | ▸ Candidate kind *: semantic; Candidate title *: (empty); What the run showed *: (empty) |
| CTUI-0426 | ACTION | Memory / Capture & Remember / Capture task outcome | FORM | ▸ Task ID *: (empty); Outcome status (success or failed) *: (empty) |
| CTUI-0427 | ACTION | Memory / Capture & Remember / Capture episode | FORM | ▸ Memory title *: (empty); episodic memory *: (empty) |
| CTUI-0428 | ACTION | Memory / Capture & Remember / Capture environment experience | FORM | ▸ Memory title *: (empty); procedural memory *: (empty) |
| CTUI-0429 | ACTION | Memory / Capture & Remember / Attach content-addressed artifact reference | FORM | ▸ Exact candidate memory ID *: (empty); Exact registered artifact ID *: (empty) |
| CTUI-0430 | GOVERNED | Memory / Capture & Remember / Post-run reflection and writeback | FORM | ▸ Terminal task in the current run *: (empty) |
| CTUI-0433 | GOVERNED | Memory / Working & Task Memory / Set slot | FORM | ▸ Exact task ID *: (empty); Working slot type *: (empty); Slot value *: (empty) |
| CTUI-0434 | GOVERNED | Memory / Working & Task Memory / Compare-and-swap update | FORM | ▸ Exact task ID *: (empty); Working slot type *: (empty); Expected slot revision *: (empty); New slot value *: (empty) |
| CTUI-0436 | GOVERNED | Memory / Working & Task Memory / Grant or revoke task-memory access | FORM | ▸ grant or revoke *: grant; Exact task ID *: (empty); Exact agent ID *: (empty) |
| CTUI-0437 | ACTION | Memory / Working & Task Memory / Refresh task memory | CONFIRM |  |
| CTUI-0438 | GOVERNED | Memory / Working & Task Memory / Promote slot to governed candidate | FORM | ▸ Exact task ID *: (empty); Exact working slot type *: (empty); Candidate kind *: semantic; Candidate title *: (empty) |
| CTUI-0439 | ACTION | Memory / Working & Task Memory / Compile provider-neutral memory handoff | FORM | ▸ Exact task ID *: (empty) |
| CTUI-0450 | GOVERNED | Memory / Review & Governance / Evidence-gated promotion | FORM | ▸ Exact memory ID *: (empty); Promotion rationale *: (empty) |
| CTUI-0456 | GOVERNED | Memory / Review & Governance / Invalidate, supersede, or tombstone | FORM | ▸ Exact memory ID to tombstone *: (empty); Reason *: (empty) |
| CTUI-0457 | DESTRUCTIVE | Memory / Review & Governance / Retention and hard purge | HIDDEN | a hard purge destroys a record irrecoverably, and the only purge code (memory/retention PurgeManager.HardPurge) drops derived index entries into an in-memory ledger while the canonical memory row stays; no governed store purge or retention-policy authority exists, so this needs an owner decision on who may purge before it is wired |
| CTUI-0458 | GOVERNED | Memory / Review & Governance / Protected-record encryption and key rotation | HIDDEN | memory/encryption Vault is an in-memory AES-GCM helper fed caller-supplied key bytes; no memory record is stored encrypted and no key custody exists outside UI state, so key rotation needs an owner decision on key storage before it is wired |
| CTUI-0461 | GOVERNED | Memory / Learning — Process 07 / Commit verified outcome | FORM | ▸ Project-relative commit input JSON (as `marshal learning commit`) *: (empty) |
| CTUI-0465 | GOVERNED | Memory / Learning — Process 07 / Invalidate learned item | FORM | ▸ Project-relative invalidation JSON (as `marshal learning invalidate`) *: (empty) |
| CTUI-0476 | GOVERNED | Memory / History & Versioning / Governed branch merge | HIDDEN | the memory/versioning branch engine is a stub: RecordBranchWrite stores nothing and MergeBranch returns a fixed record list, so no durable memory branch exists to merge; wiring it would report a merge that never happened |
| CTUI-0480 | ACTION | Memory / Import & Bootstrap / Assimilate existing project knowledge | FORM | ▸ Project-relative knowledge file *: (empty); Format (marshal-json, codex-jsonl, claude-jsonl, portable) *: marshal-json; Dry run (true/false) *: true |
| CTUI-0484 | ACTION | Memory / Import & Bootstrap / Import MARSHAL session transcript | FORM | ▸ Project-relative input file *: (empty); Dry run (true/false) *: true |
| CTUI-0485 | ACTION | Memory / Import & Bootstrap / Import Codex JSONL | FORM | ▸ Project-relative input file *: (empty); Dry run (true/false) *: true |
| CTUI-0486 | ACTION | Memory / Import & Bootstrap / Import Claude JSONL | FORM | ▸ Project-relative input file *: (empty); Dry run (true/false) *: true |
| CTUI-0487 | ACTION | Memory / Import & Bootstrap / Import Antigravity SQLite | RESULT | BLOCKED: no Antigravity history to import: ~/.gemini/antigravity-cli/conversations is unreadable (open /home/user/.cache/e2emenu/home/.gemini/antigravity-cli/conversations: no such file or directory); run … |
| CTUI-0489 | ACTION | Memory / Import & Bootstrap / Export Process 07 project/general bundle | FORM | ▸ New project-relative export file *: (empty) |
| CTUI-0491 | GOVERNED | Memory / Import & Bootstrap / Local portable memory-pack import/export | FORM | ▸ Operation (import/export) *: (empty); Project-relative pack path *: (empty); Import dry run (true/false) *: true |
| CTUI-0499 | ACTION | Memory / Status & Integrity / Rebuild derived projections | CONFIRM |  |

## Models

| ID | Type | Path | State | Detail |
|---|---|---|---|---|
| CTUI-0573 | GOVERNED | Models / Governed Optimization — Process 08 / Cycle / Start from Process 07 commit | FORM | ▸ Project-relative cycle input JSON (as `marshal optimization start`) *: (empty) |
| CTUI-0587 | GOVERNED | Models / Governed Optimization — Process 08 / Counterfactual Evaluation / Offline bounded replay | HIDDEN | owner decision: OptimizationService.ExecuteReplay/Promote/Canary take a caller-supplied optimization.Governance (governable providers, evidence freshness, independent-cluster floor, canary exposure cap, rollback requirement) and no canonical optimization service governance record exists in the store or config; no production code constructs one or calls StartCycle. A TUI form supplying those values would author the very policy that judges its own promotion. Needs an owner decision on where optimization service governance lives (P10 review 2026-11-05); the replay also needs a stored cycle and a bwrap ReplayRunner, which exists (optimization.BwrapReplayRunner) but has no production composition |
| CTUI-0617 | GOVERNED | Models / Governed Optimization — Process 08 / Promotion & Rollout / Promotion decision and evidence | HIDDEN | owner decision: OptimizationService.ExecuteReplay/Promote/Canary take a caller-supplied optimization.Governance (governable providers, evidence freshness, independent-cluster floor, canary exposure cap, rollback requirement) and no canonical optimization service governance record exists in the store or config; no production code constructs one or calls StartCycle. A TUI form supplying those values would author the very policy that judges its own promotion. Needs an owner decision on where optimization service governance lives (P10 review 2026-11-05) |
| CTUI-0621 | GOVERNED | Models / Governed Optimization — Process 08 / Promotion & Rollout / Canary creation | HIDDEN | owner decision: OptimizationService.ExecuteReplay/Promote/Canary take a caller-supplied optimization.Governance (governable providers, evidence freshness, independent-cluster floor, canary exposure cap, rollback requirement) and no canonical optimization service governance record exists in the store or config; no production code constructs one or calls StartCycle. A TUI form supplying those values would author the very policy that judges its own promotion. Needs an owner decision on where optimization service governance lives (P10 review 2026-11-05); a canary also needs a stored promotion record, which nothing produces |
| CTUI-0623 | GOVERNED | Models / Governed Optimization — Process 08 / Promotion & Rollout / Governed playbook promotion | HIDDEN | owner decision: OptimizationService.ExecuteReplay/Promote/Canary take a caller-supplied optimization.Governance (governable providers, evidence freshness, independent-cluster floor, canary exposure cap, rollback requirement) and no canonical optimization service governance record exists in the store or config; no production code constructs one or calls StartCycle. A TUI form supplying those values would author the very policy that judges its own promotion. Needs an owner decision on where optimization service governance lives (P10 review 2026-11-05); the spec additionally requires the active playbook change to be separately authorized, and no playbook-activation authority exists |
| CTUI-0626 | GOVERNED | Models / Governed Optimization — Process 08 / Process 07 Feedback / Return results to learning | HIDDEN | the spec requires optimization service evidence to write back, and no optimization service cycle, counterfactual or promotion can be produced (see CTUI-0587/0617). A typed benchmark form would record operator-invented evidence into learning (P10 review 2026-11-05) |

## Security

| ID | Type | Path | State | Detail |
|---|---|---|---|---|
| CTUI-0649 | GOVERNED | Security / Policies / Run policy test suite | FORM | ▸ Project-relative policy test suite (JSON) *: (empty) |
| CTUI-0664 | GOVERNED | Security / Authority & Capabilities / Grant capability or role | HIDDEN | owner decision: capability.Engine.Grant has only trusted in-runtime callers (task execution grants, local-control self-provisioning). A TUI grant form would make the local operator a grant issuer for arbitrary principals, kinds and scopes; which principals, capability kinds and scopes the TUI may grant, and with what expiry, needs an owner decision (P10 review 2026-11-05) |
| CTUI-0665 | DESTRUCTIVE | Security / Authority & Capabilities / Revoke capability or role | RESULT | BLOCKED: no grant is selected to revoke |

## System

| ID | Type | Path | State | Detail |
|---|---|---|---|---|
| CTUI-0705 | ACTION | System / Runtime & Store / Start local daemon | CONFIRM |  |
| CTUI-0741 | ACTION | System / MCP / Start local MCP server | FORM | ▸ Loopback listen address (host:port) *: 127.0.0.1:8080 |
| CTUI-0750 | ACTION | System / A2A / Start local A2A server | FORM | ▸ Loopback listen address (host:port) *: 127.0.0.1:8081 |
| CTUI-0761 | GOVERNED | System / Access Tokens / Create local-user token | FORM | ▸ Token name *: (empty); Capabilities *: all |
| CTUI-0762 | GOVERNED | System / Access Tokens / Create MCP-client token | FORM | ▸ Token name *: (empty); Capabilities *: status.read,task.read,agent.read,evidence.read |
| CTUI-0763 | GOVERNED | System / Access Tokens / Create A2A-agent token | FORM | ▸ Token name *: (empty); Capabilities *: status.read,task.read,handoff.create,handoff.read |
| CTUI-0767 | DESTRUCTIVE | System / Access Tokens / Revoke token | FORM | ▸ Token ID *: (empty) |
| CTUI-0769 | ACTION | System / Backup, Restore & Cleanup / Create state backup | CONFIRM |  |
| CTUI-0771 | DESTRUCTIVE | System / Backup, Restore & Cleanup / Restore state backup | FORM | ▸ Verified backup path *: (empty) |
| CTUI-0774 | DESTRUCTIVE | System / Backup, Restore & Cleanup / Garbage collect eligible artifacts | CONFIRM |  |
| CTUI-0781 | ACTION | System / Integrity, Legal & Release / SPDX SBOM generation | HIDDEN | owner decision (legal release): tools/release_trust.py generate-sbom is a repository release script that is not part of the shipped binary (a Community install has no tools/ directory) and produces release-trust artifacts; running release tooling from the TUI is a release decision, not a menu action (P10 review 2026-11-05) |
| CTUI-0782 | SENSITIVE | System / Integrity, Legal & Release / Detached key generation | HIDDEN | owner decision (key material): tools/detached_sign.py generates a detached signing key that must stay outside UI state; generating release keys from the TUI process would put secret material in the workspace's memory and terminal (P10 review 2026-11-05) |
| CTUI-0783 | SENSITIVE | System / Integrity, Legal & Release / Detached signing | HIDDEN | owner decision (key material, legal release): detached signing uses the release key from tools/detached_sign.py, which must stay outside UI state; signing release artifacts is a release decision (P10 review 2026-11-05) |
| CTUI-0785 | ACTION | System / Integrity, Legal & Release / Reproducible Linux release build | HIDDEN | owner decision (legal release): tools/build_release.sh is a repository release script, not part of the shipped binary, and builds release artifacts; running it from the TUI is a release decision. Nothing here may publish (P10 review 2026-11-05) |
| CTUI-0787 | ACTION | System / Integrity, Legal & Release / Legal evidence-pack export | FORM | ▸ New project-relative output file (.tar.gz) *: .marshal-extreme/exports/legal-evidence-pack.tar.gz |
| CTUI-0803 | ACTION | System / Exit MARSHAL | CONFIRM | refreshed |
