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
> By default, the MCP HTTP server listens on loopback (`127.0.0.1:8080`) with bearer-token authentication. Unauthenticated mode (`--insecure`) and `marshal mcp status` reject non-loopback addresses.

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
marshal auth token revoke --id <token-id>
```

---

## Project Memory System

Marshal records design artifacts, imported sessions, task outcomes and explicit memory notes in
`.marshal/state.db`. Recording a candidate does not make it available to later workers.

### The Candidate-Review Workflow

1. **Candidate ingestion**: Design artifacts, imported sessions, task outcomes and proposed notes enter memory as candidates.
2. **Review**: Candidates can be kept (promoted to durable memory) or rejected. In the default edition, the Marshal can retain notes through its bound live MCP session; those notes can be recalled without a separate operator promotion.
3. **Recall**: Later workers receive relevant durable or verified records, subject to project, scope, ACL, freshness and context budgets. Worker context excludes raw session history. MCP recall and `project_context` use the memory service; `project_context` searches using the current goal or project purpose and architecture when no query is supplied. The default edition also allows recall of candidates retained through live MCP; other candidates remain inactive.

Retrieval receipts store a query digest rather than the raw query text. An empty
`query_text` therefore does not mean the caller searched with an empty query.

### Reviewing Memory in the Board (TUI)

1. Press **`3`** on the Board to open the **Memory View**.
2. Browse pending candidates using `j` / `k` (or `↑` / `↓`).
3. Press **`a`** then **`Enter`** to promote (keep) the candidate as durable memory (`Esc` cancels).
4. Press **`r`** then **`Enter`** to reject (tombstone) the candidate (`Esc` cancels).
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

# List all project memory records with lifecycle states and content digests
marshal memory audit
```
