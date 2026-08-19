# MARSHAL Memory CLI & Operator Reference

## Synopsis
```bash
marshal memory status [--json]
marshal memory recall <query> [--json]
marshal memory show <MEM-ID> [--json]
marshal memory list [--project ID] [--json]
marshal memory promote <MEM-ID> [--dry-run] [--json]
marshal memory tombstone <MEM-ID> [--dry-run] [--json]
marshal memory audit [--task ID | --memory ID] [--json]
```

## Description
The `marshal memory` suite enables local inspection, retrieval diagnostics, and governance operations over canonical memory and derived indexes without requiring a graphical web interface.
