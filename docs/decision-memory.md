# Decision Memory and ADR Automation (T09)

Structured decision records and automated Architecture Decision Record (ADR) management.

## Statuses

- `PROPOSED`
- `ACCEPTED`
- `REJECTED`
- `SUPERSEDED`
- `DEPRECATED`

## APIs

- `Propose(ctx, id, taskID, agentID, title, context, decision)`
- `Accept(ctx, id, authorityID)`
- `Reject(ctx, id, authorityID)`
- `Supersede(ctx, oldID, newID)`
