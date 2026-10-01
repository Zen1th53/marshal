# Provider setup

Install the provider's CLI separately and sign in using its own interface.
MARSHAL uses your existing native provider configuration. Do not paste
credentials into the composer or into shared project documents.

| Provider | Binary | Qualified CLI version | Native launch | Marshal model |
| --- | --- | --- | --- | --- |
| Codex | `codex` | 0.159.2 | `marshal codex` | Yes |
| Claude Code | `claude` | 2.1.286 | `marshal claude` | Yes |
| OpenCode | `opencode` | 1.18.16 | `marshal opencode` | No |
| Antigravity | `agy` | 1.2.7 | `marshal agy` | Yes |

Qualification establishes command grammar for these versions. It does not prove
sign-in, model availability, network access or successful execution. Other
versions show unqualified operations. Antigravity's desktop application alone
does not supply the `agy` CLI.

## Check availability

```bash
marshal doctor --probe-providers
marshal adapters
```

In the TUI, `/provider status` and `/harness probe` inspect discovery.
An installed binary can still show unknown authentication or model state.
`/provider config codex` inspects configuration; credentials stay in the
provider's own login flow.

## Native sessions and governed work

```text
/codex new
/claude continue
/opencode new
/agy new
```

Exit the provider to return to MARSHAL. Native sessions require an interactive
terminal. Use `/codex cli <arguments>` (or the corresponding provider command)
for provider-owned arguments; consult that installed provider's help.

Explicit `/codex exec <prompt>` and `/claude exec <prompt>` use governed task
execution. `/opencode run <prompt>` sends an explicit run request; `/agy prompt
<prompt>` opens a native prompted conversation. A typo in a subcommand runs
nothing. Native provider permission settings and MARSHAL's governed approvals
are separate; see [concepts](concepts.md).

## Models and reasoning

`/models` lists discovered Codex models. `/model select codex <model_name>` or
`/model select claude <model_name>` chooses a catalogued model for future governed
runs. Already started runs keep their model. `/effort high` sets supported Codex
reasoning effort after selecting a model; `/effort default` restores its default.
Claude does not read that effort preference. `/harness select` does not apply an
execution preference in this build.
