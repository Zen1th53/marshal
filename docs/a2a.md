# Agent-to-Agent (A2A) Protocol Guide

**Wire Version**: `A2A-Version: 1.0`  
**Runtime Milestone**: `v0.4.0`  

SLAVES implements the Agent-to-Agent (A2A 1.0) protocol standard, allowing multi-agent platforms and external orchestrators to discover agent capabilities, exchange structured messages, and delegate tasks safely.

---

## 1. Authentication & Bearer Tokens

A2A HTTP endpoints require constant-time HMAC-validated Bearer tokens.

### Generate Token
```bash
slaves auth token create --name a2a-agent
```

### Authorization Header
```http
Authorization: Bearer $SLAVES_TOKEN
A2A-Version: 1.0
Content-Type: application/json
```

---

## 2. Running the A2A Server

### Start Server
```bash
slaves a2a serve --listen 127.0.0.1:8081
```

### Inspect Status
```bash
slaves a2a status
```

---

## 3. Endpoints & Protocol Discovery

### Agent Card Discovery (`GET /.well-known/agent-card.json`)
Returns the machine-readable A2A Agent Card defining supported roles, capabilities, and message schemas.

```bash
curl -s -H "Authorization: Bearer $SLAVES_TOKEN" \
  http://127.0.0.1:8081/.well-known/agent-card.json
```

### Message Send (`POST /message:send`)
Dispatches structured A2A task delegation messages to the SLAVES control plane.

```bash
curl -s -X POST http://127.0.0.1:8081/message:send \
  -H "Authorization: Bearer $SLAVES_TOKEN" \
  -H "A2A-Version: 1.0" \
  -H "Content-Type: application/json" \
  -d '{
    "message_id": "MSG-1001",
    "sender": { "id": "RemoteOrchestrator", "role": "architect" },
    "recipient": { "id": "SLAVESControlPlane", "role": "developer" },
    "parts": [
      {
        "type": "task_delegation",
        "data": {
          "task_id": "TASK-001",
          "action": "execute",
          "adapter": "codex"
        }
      }
    ]
  }'
```

---

## 4. Remote Role Spoofing Prevention

SLAVES enforces strict role boundaries on A2A delegation:
- **No Self-Approval**: A remote agent claiming the `developer` role cannot issue `qa` or `security` approval messages.
- **Token Scope Binding**: Bearer tokens are validated against assigned principal permissions before any worktree operation begins.
