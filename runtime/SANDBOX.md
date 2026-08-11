# Worker Sandbox Contract

## Mission

Limit a worker to the capabilities required for its task.

## Isolation Dimensions

- filesystem,
- repository/worktree,
- process,
- environment variables,
- network,
- CPU,
- memory,
- wall time,
- open files,
- device access,
- credentials.

## Default Local Sandbox

Recommended first implementation:

```text
Git worktree
+ dedicated process
+ task-scoped environment
+ no production credentials
+ bounded timeout
+ optional network restrictions
```

Containers/VMs are optional stronger adapters.

## Filesystem

Worker receives:
- task worktree,
- explicitly required cache/temp paths.

Worker should not receive arbitrary home-directory write access by default.

## Network

Default according to task:

```text
deny or restricted
```

Enable internet/external services only when required.

## Secrets

Injected ephemerally by Secrets Broker.

Never written into:
- memory files,
- command history,
- general logs,
- semantic index.

## Termination

On worker termination:
- capture exit status,
- preserve task-scoped logs/evidence,
- revoke secret leases,
- update session state,
- do not mark task complete automatically.
