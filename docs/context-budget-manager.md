# Context Budget Manager (T12)

Dynamically manages context token allocation across priority sections, dropping or compacting non-mandatory content to fit budget limits.

## APIs

- `Allocate(ctx, budget, sections)` -> `Decision`
