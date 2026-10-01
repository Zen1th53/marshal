# Continue past conversations

MARSHAL keeps track of two kinds of past work in this project. To see both:

```text
/sessions
```

- **Native conversations** are chats you had with an agent directly, for
  example through `/codex` or `/claude`.
- **Governed runs** are work MARSHAL ran for you, for example from a plan.

## Continue the latest conversation

```text
/codex resume --last
/claude continue
/opencode resume --last
/agy resume --last
```

"Latest" means the latest conversation **in this project** with that agent.
MARSHAL never picks up a conversation from a different project.

## Continue a specific conversation

Copy the conversation's ID from `/sessions` and use it:

```text
/codex resume CONVERSATION-ID
```

## Search what was said before

MARSHAL remembers what was said in your agent conversations, without the
agents' hidden reasoning, and with secrets filtered out. You can search it:

```text
/memory search error handling
/memory list
```

Treat these memories as notes, not facts. Check important details against
your project.

## Apply a Codex cloud task

If you used Codex's cloud tasks, you can bring a task's changes into your
project:

```text
/apply CODEX-TASK-ID
```

Use the task ID that Codex gives you. MARSHAL saves a checkpoint first, then
shows exactly which files changed. If you do not like the result, roll back to
the checkpoint it names. See [Undo changes](undo.md).

!!! note
    `/apply` is only for Codex's own task IDs. For work from a MARSHAL plan,
    use `/diff` and `/marshal accept` instead.
