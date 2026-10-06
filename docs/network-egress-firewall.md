# Network egress enforcement

Governed provider workers on Linux always run in a bubblewrap network namespace
with `--unshare-net`. They have no direct host network. The runtime owns an HTTP
proxy outside that namespace for each worker run, listening on a private Unix
socket. The socket is bound into the sandbox; a trusted `/usr/bin/socat` or
`/bin/socat` bridges sandbox loopback to it. Uppercase and lowercase HTTP, HTTPS
and ALL proxy variables point to that loopback listener; NO_PROXY is empty.
HTTPS uses CONNECT and plain HTTP uses forward requests. Clients that ignore
proxy settings cannot reach the host network.

An inherited Linux amd64 seccomp filter denies host pathname Unix sockets,
raw/UDP/DNS socket creation, direct connects, Fast Open, and alternate socket
APIs. A host supervisor records refusals before returning EACCES. The bridge
and bootstrap shell are hidden in a separate PID namespace; the worker cannot
obtain their descriptors or modify their memory. Missing syscall supervision
refuses launch. This covers sockets created after launch in the worktree, Git
metadata, system trees, and explicit binds.

Governed hand-in and integration checks use the same observed sandbox, each
with a new operator-visible run linked to the plan run and an empty default
allowlist. They inherit no host proxy environment. Automatic Git bookkeeping
disables hooks, filesystem monitors, executable filters, external diff/textconv
commands, custom merge drivers, submodule recursion, and automatic lazy
fetching. Git transports are disabled for this bookkeeping. Operator-opened native
checks retain their separate execution path.

The default allowlist contains only the selected provider API endpoint and port:
Codex `api.openai.com:443`, Claude `api.anthropic.com:443`, Gemini
`generativelanguage.googleapis.com:443`. OpenCode selects the API by the model's
provider prefix: openai, anthropic, deepseek (`api.deepseek.com:443`), google, or
ollama (`127.0.0.1:11434`). An unknown OpenCode provider fails closed. Alternative
API hosts require an operator grant. Host provider configuration and credentials
are not imported by this change.

The canonical network capability policy still applies. A governed provider run
needs its own API even without a caller-supplied `network_required` flag. Run
request rules cannot grant extra destinations. Non-network sandbox work keeps
its isolated namespace and does not require the proxy or bridge.

## Operator decisions

1. A refused request returns HTTP 403 and is recorded as run evidence. The TUI
   displays `<worker> wants to reach <host:port>. Allow?`, its worker run ID, and
   the command to grant it. `/egress status` lists active runs, allowed endpoints,
   and refused destinations, plus the durable refusal inbox for completed runs.
   Process 05 worker runs also show their parent run.
2. Every runtime first persists `network.egress.notification` in its operator
   queue, readable through `/egress status` even without live delivery. With
   a TUI attached the request is also delivered to `.marshal/inbox/marshal.md`. Marshal chat is told
   to re-read that inbox and relay requests. Delivery is a pull; an active native
   chat is not interrupted.
3. The operator types `/egress allow <run-id> <host[:port]>` in the TUI to grant,
   or `/egress revoke <run-id> <host[:port]>` to revoke. An omitted port means 443.
   Only the authenticated local operator context with `egress.decide` authority
   can mutate the list. Model output, including Marshal text, cannot grant.
4. The worker may retry the refused request. A grant does not replay a request
   or restart a provider that already exited. Revocation also closes existing
   forwards and tunnels to that exact endpoint.
5. All grants expire when the worker run ends. Each retry is a new run; no grant
   is restored after restart. Grants/revocations record the operator identity,
   endpoint, run, and UTC time in structured events.

Matching is exact after case, trailing-dot and standard IP normalization. There
are no wildcard or suffix grants, DNS names do not grant literal IP authority,
and ports are separate grants. Name resolution is performed outside the sandbox,
then the proxy dials the validated IP. Existing protections against names
resolving to private/local addresses remain; local services require exact IP
rules. Redirect destinations must pass a fresh proxy check.

Every valid attempt is stored in `egress_decisions`, linked to the worker run,
and emits network decision events. Run evidence also records the endpoint and
result, including HTTP parser refusals and direct socket refusals. UDP/DNS and
Unix sockets are refused at creation; evidence records family/type rather than
a destination that has not yet been supplied. Direct TCP evidence includes
the destination when it can be read; unreadable requests are still refused. Request payloads, URL paths and
credentials are not logged. Evidence persistence or notification failure refuses the connection; a failed
socket observer terminates the governed process.

## Availability and scope

The runtime probes the actual bubblewrap network namespace and checks the
trusted bridge executable. Missing isolation, bridge, Unix socket, or bridge
startup refuses network work. There is no unrestricted-network fallback. The
sandbox launch checks Unix connectivity and bridge readiness before starting
its worker. Proxy/bridge loss leaves the namespace isolated.

Native sessions opened directly by the operator are out of scope and reported
that way in status. macOS is out of scope. Provider CLI support for standard
proxy variables and provider authentication still determine whether an actual
provider session succeeds; local integration tests do not establish live API
compatibility.
