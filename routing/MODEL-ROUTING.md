# Capability-Based Model / Agent Routing

## Mission

Choose an agent/model because the task requires its capabilities, not because a
vendor name is fashionable.

## Inputs

```text
role
task risk
required capabilities
context size
tool requirements
latency target
cost budget
independence requirement
installed adapter capability
```

## Registry

`routing/CAPABILITY-SCHEMA.json` defines capability dimensions.

A runtime may maintain current model/provider records:

```json
{
  "model_id": "provider/model",
  "adapter": "codex",
  "capabilities": {
    "coding": "high",
    "structured_output": "high"
  },
  "verified_at": "..."
}
```

Do not hardcode volatile rankings into the shared doctrine.

## Independent Review

For QA/AppSec independence, prefer a separate session and, when useful, a
different model/provider.

Different model is not mandatory when it adds no value; independent evidence is.

## Fallback

If preferred model unavailable:

```text
find next profile match
→ disclose capability downgrade
→ preserve required gates
```

Do not lower a required security/verification property merely to use a cheaper model.

## Evaluation-Driven Updates

Update routing data from:
- conformance results,
- project-specific evals,
- measured latency/cost,
- observed failure modes.

Hype/stars/social claims do not update routing scores.
