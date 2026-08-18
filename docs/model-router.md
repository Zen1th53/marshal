# Model Router (T15)

Routes task execution requests across first-class AI model providers (codex, claude, gemini, opencode) based on capability scoring, context limits, and cost/latency classes.

## APIs

- `Route(ctx, requiredCaps, minContext)` -> `RouteDecision`
