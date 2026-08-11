# OWNERSHIP.md — Component and Review Ownership Map

## Purpose

Route decisions and reviews to the right owner without making every agent load every domain.

## Record

```yaml
component: path/or/domain
technical_owner: unknown
architecture_owner: architect
qa_owner: qa
security_owner: appsec
human_owner: null
required_review_for:
  - public_contract_change
  - migration
notes: []
last_verified: null
```

## Rules

- Repository-native CODEOWNERS or governance overrides this file.
- Missing owner is not permission to self-approve.
- Ownership should follow actual responsibility, not title inflation.
- Keep component boundaries coarse enough to remain maintainable.

## Current Ownership

_No project-specific ownership recorded._
