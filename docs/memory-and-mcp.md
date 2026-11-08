# Memory and MCP Usage

Marshal includes a persistent project memory system and an integrated Model Context Protocol (MCP) server. Together, they allow AI agents to retain project conventions, architectural decisions, and domain context across runs.

---

## Model Context Protocol (MCP) Server

Marshal implements an MCP server enabling IDEs, developer tools, and external agent CLIs to connect directly to the Marshal runtime.

### Starting the MCP Server

You can run the MCP server over a local network port or standard input/output (stdio):

```bash
# Start an HTTP MCP server listening on local loopback
marshal mcp serve --listen 127.0.0.1:8080

# Start a stdio MCP server for direct subprocess integration
marshal mcp serve --stdio --role marshal
```

> [!NOTE]
> For security, the MCP HTTP server binds only to loopback addresses (`127.0.0.1`, `::1`). Non-loopback bindings are rejected.

### Checking MCP Server Status

Verify that the local MCP server is running and responding:

```bash
marshal mcp status
```

### Authentication Tokens

By default, the HTTP MCP server requires authentication using bearer tokens:

```bash
# Generate a token for an external client
marshal auth token create --name ide-extension

# List existing tokens
marshal auth token list

# Revoke a token
marshal auth token revoke <token-id>
```

---

## Project Memory System

During runs, agents learn important project facts, architectural constraints, and recurring conventions. Marshal stores this knowledge in `.marshal/state.db`.

### The Candidate-Review Workflow

To prevent AI agents from polluting memory with incorrect assumptions or temporary debug notes, Marshal uses a **candidate-review model**:

1. **Candidate Ingestion**: When an agent suggests new knowledge or records an architectural decision, Marshal writes it as an unverified **candidate** (`MemoryCandidate`).
2. **Operator Review**: Candidates do not enter active recall until reviewed by a human operator.
3. **Promotion**: Once promoted, the memory becomes active, searchable, and recallable by future Marshal runs.

### Reviewing Memory in the Board (TUI)

1. Press **`3`** on the Board to open the **Memory View**.
2. Browse pending candidates using `j` / `k` (or `↑` / `↓`).
3. Press **`a`** to promote (keep) the candidate as permanent memory.
4. Press **`r`** to reject (tombstone) the candidate.
5. Press **`i`** to inspect the candidate's provenance and supporting evidence.
6. Press **`1`** or **`Esc`** to return to the Board.

---

## CLI Memory Commands

You can also inspect and manage memory directly from your terminal:

```bash
# Check memory system health and record count
marshal memory status

# List all active promoted memory records
marshal memory list

# List pending unverified memory candidates
marshal memory candidates

# Search memory using a natural language query
marshal memory recall "how is authentication handled"

# Show details of a specific memory record
marshal memory show MEM-12345

# Manually record an explicit project convention
marshal memory remember "API Style" "All endpoints must return JSON with camelCase keys"

# Promote a candidate to active memory
marshal memory promote MEM-12345

# Tombstone (deactivate) an outdated or incorrect memory record
marshal memory tombstone MEM-12345

# View the audit trail and history of a memory record
marshal memory audit MEM-12345
```
