# ENVIRONMENT.md — Reproducible Environment Record

## Purpose

Record the environment assumptions required to build, test, run, or verify the project.

Do not record secrets.

## Canonical Shape

```yaml
repository:
  root: unknown
  expected_default_branch: unknown

runtime:
  language: unknown
  version: unknown

package_manager:
  name: unknown
  version: unknown

system_tools: []

services: []

commands:
  bootstrap: []
  build: []
  test: []
  lint: []
  typecheck: []
  security: []

platforms:
  supported: []
  verified: []

fixtures:
  required: []

environment_variables:
  required_names: []
  secret_names: []

last_verified:
  commit: null
  timestamp: null
  agent: null
```

## Rule

Environment facts should be discovered from repository-native files and fresh commands, not guessed from memory.
