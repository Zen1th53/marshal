# DATA-POLICY.md — Data Classification and Retention Defaults

## Classes

### PUBLIC
Safe for public repository/docs.

### INTERNAL
Project/team data not intended for public release.

### CONFIDENTIAL
Sensitive business/security data requiring controlled access.

### SECRET
Credentials, private keys, bearer tokens, recovery material, production secrets.

## Default Handling

| Class | General Memory | Semantic Index | Logs | External Tools |
|---|---|---|---|---|
| PUBLIC | allowed | allowed | allowed | task-dependent |
| INTERNAL | allowed scoped | scoped | minimal | policy-dependent |
| CONFIDENTIAL | minimal/scoped | default deny | redacted/minimal | explicit permission |
| SECRET | forbidden | forbidden | forbidden | forbidden unless dedicated secret workflow |

## Retention

Project policy owns exact durations.

Default principle:

```text
retain only while it has operational, legal, security, or engineering value
```

Do not retain raw transcripts merely because storage is cheap.
