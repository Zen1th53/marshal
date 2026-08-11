# Project Bootstrap

The bootstrap layer adapts this reusable pack to an existing repository without
rewriting the repository's native governance.

Use:

```bash
python tools/agentos.py detect-project /path/to/repo
```

Then follow `bootstrap/PROJECT-INIT.md`.

Bootstrap is discovery-first. It does not invent build/test commands or overwrite
an existing `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, or other native file blindly.
