# Sessions and applying changes

`/sessions` separates **NATIVE conversations** from **GOVERNED runs**.
Provider session commands filter that inventory by provider. Imported conversation
memory is not proof that a provider can resume the original session.

```text
/codex resume --last
/claude continue
/opencode resume --last
/agy resume --last
```

Latest-session selection is scoped to the current project and provider.
Copy an inventory ID to choose an explicit conversation. A conversation belonging
to another project or provider is refused. Native provider pickers remain
available through the provider's `cli` command.

MARSHAL captures visible conversation and bounded tool evidence from supported
local native histories. Hidden reasoning is excluded and imported records pass
secret redaction/filtering. Imported memory is candidate information, not verified
fact. `/memory list` and `/memory search <query>` find it. A new session does not
receive the entire memory store automatically.

## Apply a Codex task

`/apply <codex_task_id>` requires a task ID supplied by Codex itself. For example,
`/apply CODEX_TASK_ID` uses that value after you replace it with your Codex ID.
It never guesses an ID from a MARSHAL run; MARSHAL task IDs are refused.

An attached runtime snapshots the project before invoking Codex apply. The result
lists files that actually changed; a successful provider exit with no changed
files is reported as such. Use the reported `/rollback` checkpoint to undo changes.
For governed Marshal tasks, review with `/diff` and decide with
`/marshal accept <task>` instead.
