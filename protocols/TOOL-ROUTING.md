# TOOL-ROUTING.md — Tool Selection and Fallback Protocol

## Mission

Choose the smallest tool that produces trustworthy evidence.

## Order

Prefer:

```text
repository-native command
→ standard local tool
→ project-approved specialist tool
→ external service only when required
```

## Tool Selection Questions

- What claim must this tool prove?
- Can a simpler local method prove it?
- Does the tool need network or secrets?
- Does it mutate state?
- Is output deterministic/reproducible?
- Does failure block the task or only reduce confidence?

## Fallback

A fallback must preserve semantics.

Bad:

```text
security scanner unavailable
→ skip security review and mark PASS
```

Good:

```text
scanner unavailable
→ perform manual/static checks in scope
→ mark scanner-specific coverage UNVERIFIED
```

## Tool Output Trust

Tool output is evidence, not authority.

- Linter success does not prove runtime correctness.
- Scanner success does not prove security.
- AI-generated analysis does not prove repository state.
- Search result relevance does not prove source truth.

## External Tools

Before uploading code/data externally, apply:

- `CAPABILITIES.yaml`,
- `protocols/DATA-GOVERNANCE.md`,
- repository policy,
- approval if required.

## Tool Hallucination Rule

Do not claim a tool, command, integration, or service exists until discovered or verified in the current environment.
