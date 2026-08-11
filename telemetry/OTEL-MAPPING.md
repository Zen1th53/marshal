# OpenTelemetry Mapping

## Resource Attributes

Recommended custom attributes:

```text
agentos.pack.version
agentos.runtime.version
agentos.project.id
agentos.tenant.id
agentos.adapter.name
agentos.adapter.version
```

## Span Attributes

```text
agentos.agent.id
agentos.session.id
agentos.task.id
agentos.role
agentos.operation
agentos.policy.decision
agentos.approval.id
agentos.artifact.id
agentos.repository.commit
agentos.conformance.scenario
```

When current OpenTelemetry GenAI conventions provide a semantically matching,
stable/development attribute, an implementation may dual-emit it.

Do not rename Agent OS canonical state fields every time external semantic
conventions evolve.

## CLI

For `agentctl` and short-lived adapter processes, follow OpenTelemetry CLI
conventions where practical.

## Privacy

Never emit:
- chain-of-thought,
- secret values,
- full confidential prompts by default,
- raw source code unless explicit telemetry policy permits it.
