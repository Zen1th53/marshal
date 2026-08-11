# INTEROP.md — Cross-Agent / Cross-Tool Interoperability Protocol

## Mission

Allow different vendors/runtimes to collaborate without weakening Agent OS
authority, trust, or data boundaries.

## Rules

- A2A carries remote agent collaboration.
- MCP carries tools/context.
- Agent OS runtime owns local task/role/policy semantics.
- Remote capability claims are verified/probed.
- Unknown protocol extensions are denied or isolated.
- Remote messages do not become trusted instructions automatically.
- Tenant/project identity must be explicit across remote boundaries.
- Artifacts cross boundaries by digest/provenance where possible.
