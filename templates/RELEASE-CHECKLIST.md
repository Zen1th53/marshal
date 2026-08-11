# Release Checklist: <release>

## Artifact / Version

## Code

- [ ] final diff reviewed
- [ ] build/package succeeds
- [ ] quality gates pass
- [ ] no unrelated changes

## QA

- [ ] acceptance verified
- [ ] regression verified
- [ ] compatibility verified if required
- [ ] QA verdict recorded

## Security

- [ ] AppSec gate complete if required
- [ ] no unresolved BLOCKER/HIGH without formal exception
- [ ] secrets/config reviewed

## Migration

- [ ] old-state upgrade tested if applicable
- [ ] verification defined
- [ ] rollback limit known

## Operations

- [ ] config/secrets ready
- [ ] health/monitoring ready
- [ ] rollback trigger defined
- [ ] rollback steps ready

## Known Risks

## Verdict

READY | READY WITH ACCEPTED RISK | NOT READY | BLOCKED
