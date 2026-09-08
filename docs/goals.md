# Goals: what MARSHAL understood, and what it will ask about

This describes how a request becomes a Goal, how MARSHAL assesses it, and who
has to confirm it before work begins.

## The rule

**You say what you want. MARSHAL interprets it, keeps your constraints,
assesses it, and only then can planning start.**

A model may propose an interpretation. MARSHAL forms the Goal. That difference
is not stylistic: if a model's paraphrase became the Goal directly, every later
revision would paraphrase the paraphrase, and after a few rounds nothing would
record what you actually asked for.

So your original words are kept byte for byte and survive every revision.

## Complexity is not risk

These are different questions, and one number cannot answer both:

| Request | Effort | Danger |
|---|---|---|
| Rewrite the reporting module | High | Low — fully reversible, stays in the project |
| Change one production credential | Trivial | High — reaches outside, hard to undo |

A single risk score would rate the first higher than the second, which is
exactly backwards. So MARSHAL assesses ten dimensions independently and never
reduces them to a score.

**Effort:** complexity, ambiguity, dependency depth, verification difficulty.

**Danger:** blast radius, privilege, reversibility, external effects, data
sensitivity, operational criticality.

Only the danger dimensions drive confirmation. Being asked to approve something
because it is *large* is how a confirmation prompt stops meaning anything.

### Levels

`NONE` · `LOW` · `MEDIUM` · `HIGH` · `UNKNOWN`

`UNKNOWN` ranks alongside `HIGH`, not alongside `NONE`. A dimension nobody could
establish must not be the reason a request reads as safe. If MARSHAL cannot
work out how far a change reaches, it asks rather than assuming.

There are no percentages. Each finding cites the phrase behind it, so an
assessment can be inspected rather than taken on faith.

## Your constraints are kept

Limits you state are extracted from your request **before any model sees it** —
a constraint a model never had a chance to drop cannot be dropped by it.

> "Add caching. **Do not** modify the database schema. **Only** touch the
> handlers package."

Both limits become hard constraints attributed to you. They survive advisories
that argue against them, they survive revisions, and an agent cannot remove or
weaken them.

## What a model can and cannot do

A model's interpretation is read **after** the deterministic assessment is
complete, and every path it can take either adds information or increases
caution.

| It can | It cannot |
|---|---|
| Offer a clearer interpretation | Overwrite your original words |
| Record its assumptions | Turn an assumption into a fact |
| Raise a question | Answer a question on your behalf |
| Ask for approval | Say approval is unnecessary |
| Raise a risk dimension | Lower one |

A model that finds one of your constraints inconvenient records an assumption.
The constraint stays. An interpretation formed under different constitutional
rules is discarded rather than partly applied.

## Clarification

MARSHAL asks only when the answer would change something material.

- *"clean up the formatting in the parser"* — vague, but safe, narrow and
  reversible. Guessing costs little and is easily corrected. **No question.**
- *"delete the old stuff from production, or something"* — vague, and the
  answer changes what gets destroyed. **Question.**

Asking about everything teaches people to stop reading the questions.

## Who confirms

| Situation | Standard | ULTRA, Execution off | ULTRA, Execution on |
|---|---|---|---|
| Routine work | Proceeds | Proceeds | Proceeds |
| Flagged work | You confirm | You confirm | Delegated |
| **Hard approval** | **You confirm** | **You confirm** | **You confirm** |
| Open questions | You answer | You answer | You answer |
| Something unestablished | You confirm | You confirm | You confirm |

A **hard approval** is required when work cannot be undone, reaches outside the
project, touches sensitive data, needs elevated access, or affects a system
people depend on. These are decided before mode is even consulted, so no
combination of entitlement and settings can delegate one.

ULTRA raises capability. It never lowers a guarantee.

### Delegated is not approved

A delegated decision is recorded as `DELEGATED`, never `APPROVED`. When a
result is reviewed later, "you approved this" and "your settings allowed this"
are different claims, and MARSHAL keeps them distinguishable.

## Goal states

| State | Planning may start |
|---|---|
| `PENDING` | No — awaiting your decision |
| `NEEDS_INPUT` | No — a question is open |
| `APPROVED` | Yes |
| `DELEGATED` | Yes |
| `CANCELLED` | No |

A Goal with open questions cannot be approved: approving it would approve
whatever MARSHAL happened to guess. A revised Goal returns to `PENDING`,
because you agreed to the previous wording, not this one.

## Commands

```
marshal goal <request>          form a Goal and see the assessment
marshal goal explain <request>  the same, without acting
```

## Current limits

Goal intake forms and assesses; it does not yet persist Goals to the store.
The schema columns for that will be added when the code that reads and writes
them exists — adding them earlier costs CI time on every test run and buys
nothing.
