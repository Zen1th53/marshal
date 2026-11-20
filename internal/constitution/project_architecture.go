package constitution

// ProjectArchitectureInstructions is shared with the native briefing so a new
// goal cannot quietly replace the accepted project with a small isolated plan.
const ProjectArchitectureInstructions = `
ONE LIVING PROJECT ARCHITECTURE
The architecture is never in this briefing: MARSHAL keeps it in its store.
Read the current revision with marshal_project_contract_context before you
draft or plan, and again before each new draft.
Submit the next revision of the one project contract with
marshal_project_contract_submit. A goal changes the project architecture;
it never gets a fresh per-goal contract. Read the accepted contract first with
marshal_project_contract_read and preserve its ID and every existing component.
Keep unchanged parts in the submission. To remove a component, keep its entry
with removed:true, and explain the removal in revision_reason. Set revision to
the accepted revision plus one and supersedes_digest to its exact digest.
When no architecture is accepted, first describe the whole project's top-level
code and package layout, using the project knowledge index or repository
evidence. A baseline covering only the goal's part is refused.
A submitted contract is refused unless it meets these rules:
- Responsibility: every component's responsibility says what it is responsible
  for.
- Describe every connection's carries: every dependency has a
  contract.connections entry
  {"from":"<component id>","to":"<dependency id>","carries":"<what flows>"}
  whose carries is not empty and says what flows from its source to its
  target, such as review comments or layout JSON. Keep dependencies as well;
  dependencies alone do not describe what flows. Do not invent a flow; inspect
  the code or ask a concrete question. A removed component's connections need
  no carries.
- Tie each confirmed requirement to a named component: every active goal
  success criterion and confirmed requirement appears in the covers list of at
  least one component. Copy the full text: goal criteria, confirmed goal
  scope/constraints/do-not-do, and every filled line of the confirmed
  requirements card (without its caption). There is no minimum number of
  components: coverage decides. Record that mapping in REQUIREMENTS.md before
  planning tasks.
If refused, the error names what to fix; fix it and submit again.
The browser compares the proposal with the accepted revision: existing parts
remain solid; new parts and their connections are dashed; changed parts show
before and after; removed parts stay visible as faded removals. All baseline
parts are new. Every revision needs the owner's exact browser Accept.
The owner can press a on the Board or use /marshal architecture to inspect the
project at any time. Accepted revisions remain in history with their date,
goal, run and who accepted. Never describe submission or inspection as acceptance.
After a run, compare changed component paths with the accepted architecture;
report built parts and any code that differs from the architecture explicitly.
`
