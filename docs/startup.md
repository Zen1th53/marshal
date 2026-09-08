# Startup and the control center

This describes what happens when you run `marshal`, and the reasoning behind
it. It is the user-facing companion to `internal/startup`.

## The rule

**Starting MARSHAL is entry into a control center, not an environment
prerequisite test.**

If MARSHAL's own core can run, the control center opens — even when Git,
project tooling, a harness, a provider, the sandbox or the network are missing
or degraded. Those failures reduce what can be *executed*. They do not remove
the surfaces you need in order to understand and fix them.

The reason is simple: a tool that refuses to start until its environment is
already correct is least available exactly when it is most needed.

## What can and cannot stop startup

Only one class of failure closes the control center: MARSHAL having nowhere to
keep its own state. Everything else — no repository, no Git at all, no
sandbox, no policy, an unreadable database, an interrupted session — opens the
control center and is reported there.

These surfaces are always available short of that core failure:

Setup · Doctor · Help · project inspection · history · recovery

They are the tools for repairing the very problems a check might report, so
gating them on the environment being healthy would be self-defeating.

## Three separate health questions

| Dimension | Question |
|---|---|
| Core | Can MARSHAL run its control center and keep its state? |
| Environment | Are the tooling and security prerequisites present? |
| Project | Is there a usable project here? |
| Execution | Can work run under policy right now? |

Keeping these apart is what stops a missing optional tool from becoming a
refusal to start.

## Health states

| State | Meaning |
|---|---|
| `READY` | Checked and working. |
| `LIMITED` | Working, with a named restriction. |
| `NEEDS_ATTENTION` | Present but not working properly. |
| `MISSING` | Absent. Normal for an optional capability. |
| `BROKEN` | Present but unusable. |
| `OPTIONAL` | Absent and not needed for what you are doing. |
| `UNKNOWN` | Could not be checked. |

**UNKNOWN is never Ready.** A check that could not run tells you nothing, and
treating silence as success is how a status screen ends up lying.

## Startup phases

| Phase | Control center | Work runs |
|---|---|---|
| `READY` | yes | yes |
| `LIMITED` | yes | yes — some capability is unavailable |
| `NEEDS_ATTENTION` | yes | yes |
| `RECOVERY_AVAILABLE` | yes | after you decide about the interrupted work |
| `EXECUTION_BLOCKED` | yes | no |
| `CORE_FAILED` | no | no |

`LIMITED` permits work. Withholding local work because, say, outbound access
cannot be restricted would punish you for a limitation that does not affect
what you are doing. Which specific capabilities are available is answered per
capability, not by the phase.

## Setup, Doctor and repair

| | Assesses | Explains | Changes things |
|---|---|---|---|
| **Setup** | yes | — | never |
| **Doctor** | yes | yes | only with your consent |
| **Repair** | — | — | yes, then re-tests |

`Fixed` is only reported after the failing check has been re-run and passed. A
repair whose command exited successfully has demonstrated that an action was
taken, not that the problem is gone.

MARSHAL will not, on its own: run `git init`, install a dependency, write a
capability policy, provision a sandbox, enable ULTRA, or delete state. Those
are your decisions. It will explain each of them.

## Degraded operation

A failing check disables exactly the capabilities it declared and nothing else.

| Missing | Disables | Still works |
|---|---|---|
| Git | Project execution | Everything else, including Doctor and Help |
| Sandbox | Project and provider execution | Inspection, history, recovery |
| Egress enforcement | Network and provider execution | All local work |
| Capability policy | Project execution | Inspection and repair surfaces |
| A harness | That provider only | Other providers, all local work |

A missing capability policy blocks rather than falling back to a permissive
default: silently substituting one turns a configuration mistake into an
unnoticed loss of enforcement.

## Interrupted work

If a previous run ended unexpectedly, MARSHAL reclaims tasks held by workers
that are no longer running, closes sessions left open, and marks in-flight runs
failed. It then **tells you what it found**.

It does not resume anything. Recovery is offered; taking it is your decision.

## Provider readiness

Four separate facts, never collapsed into one:

1. the harness is installed;
2. a credential is available;
3. the harness can reach its service on its own;
4. MARSHAL can run work through it under policy.

Only the fourth means the provider is usable here. MARSHAL does not claim it
without having done it — standalone connectivity is not end-to-end
qualification.

## Standard and ULTRA

Both are bound by the same constitution (see Process 00). ULTRA adds depth,
parallelism and autonomy; it never removes a safety, approval or evidence
requirement that applies in Standard. It requires a valid, unexpired
entitlement and is never switched on by default.

## Privacy

Startup records what it checked and what it found, so problems can be
diagnosed later. It does not record file contents, prompts or credentials.
User-facing status text is screened for the shapes internal errors take, so a
subprocess message or a database driver string does not reach you in place of
an explanation.

## Commands

```
marshal              enter the control center
marshal tui          the same, explicitly
marshal setup        report readiness; changes nothing
marshal doctor       diagnose; repairs only with consent
marshal help         explain startup, health and modes
marshal help why     explain what is blocking work right now
```
