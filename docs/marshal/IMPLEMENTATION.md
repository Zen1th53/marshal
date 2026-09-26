# Marshal mode implementation

Marshal mode gives one appointed model responsibility for planning a goal, dispatching scoped tasks, judging hand-ins, integrating accepted work and reporting the result. The model proposes decisions; the runtime validates them against the approved plan and the constitutional gate. A person approves the plan and authorizes the final target-branch move, either at close or through standing close authorization bound to the plan digest.

## Components

- `internal/tui/commands_marshal.go` handles `/marshal <goal>`, approval, status, settings and run controls. `marshal_panel.go` renders the run snapshot. Planning and execution publish updates from background work.
- `internal/app/marshal*.go` contains `MarshalService`, the mutation boundary for recommendation, drafting, approval, dispatch, review, merge, verification, resume and close. `model_inventory.go` provides cached and static model knowledge without probing Claude.
- `internal/marshal` defines `Run`, `Task`, `HandIn`, `Review`, settings, budgets, state transitions, rework policy, amendment classification, recommender and Standard/ULTRA tier policy. `internal/marshal/driver` launches headless workers, assembles hand-ins from git and reruns approved checks.
- `internal/plan` owns the approved task graph and scoped amendment. `AmendScoped` preserves approval only when each original task's scope and checks remain covered; major changes return to user approval.
- `internal/worktree` owns task branches and resumable worktrees. `internal/execution` supplies the Process 05 preserve-branch delivery path: it commits the task result and keeps the worktree for review.
- `internal/constitution` decides task acceptance from criteria, fresh evidence, independent review and human authority. `internal/verification` evaluates the integrated result.
- `internal/store/marshal*.go` persists runs, tasks, hand-ins, reviews, settings and decisions. `internal/events` defines the decision vocabulary; `internal/provenance` seals accepted records.

## From command to close

1. `/marshal <goal>` selects or recommends a model from known inventory and opens a plan dialogue. The draft contains task scope, criteria, executable checks, assignments and budget ceilings.
2. The user approves the plan. Approval locks its scope digest. In `marshal` acceptance mode, the user can grant standing close authorization for that digest.
3. The service dispatches ready tasks to separate branches and worktrees. Standard allows one worker; ULTRA permits configured concurrency and requires a different-provider cross-review and independent verification. Workers run headlessly.
4. A driver collects the result commit, diff and touched files from git. It keeps worker-reported actions separate from runtime observations, then reruns approved checks against the result. The Marshal's review is advisory; the constitutional gate decides acceptance. Returns go to the same worker twice, then reassignment, then escalation.
5. Accepted commits merge in plan order into an integration branch. A conflict returns the task against the new integration head. Integrated checks and goal verification run before close.
6. Close uses the verified commit and the project's recorded target branch. It requires current user authority and records a checkpoint before the run becomes closed. Durable state supports restart and resume.

Runtime git operations disable repository hooks and external diff execution; hand-in validation rejects custom merge drivers. Checks use isolated checkouts so one command cannot alter another command's evidence. The target branch moves only at close.
