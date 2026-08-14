# OpenTelemetry Mapping

## Resource Attributes

Recommended custom attributes:

```text
marshal.pack.version
marshal.runtime.version
marshal.project.id
marshal.tenant.id
marshal.adapter.name
marshal.adapter.version
```

## Span Attributes

```text
marshal.agent.id
marshal.session.id
marshal.task.id
marshal.role
marshal.operation
marshal.policy.decision
marshal.approval.id
marshal.artifact.id
marshal.repository.commit
marshal.conformance.scenario
```

When current OpenTelemetry GenAI conventions provide a semantically matching,
stable/development attribute, an implementation may dual-emit it.

Do not rename MARSHAL canonical state fields every time external semantic
conventions evolve.

## CLI

For `marshal` and short-lived adapter processes, follow OpenTelemetry CLI
conventions where practical.

## Privacy

Never emit:
- chain-of-thought,
- secret values,
- full confidential prompts by default,
- raw source code unless explicit telemetry policy permits it.
