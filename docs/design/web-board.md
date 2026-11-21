# Web Board

The Web Board is the Marshal Board in a local browser. The terminal Board
stays as it is; the Web Board is a second view of the same data, easier to
read and usable from a phone through the owner's own remote desktop.

## Phase 1 (v0.3.6): read-only live view

What ships:

- `internal/webboard`: the wire form of the Board v2 view model (`vm.Board`
  to `webboard.Frame`, `"v":1`, times RFC 3339 with zone, durations in whole
  seconds), the HTTP server, and the page (`page/index.html`, `app.css`,
  `app.js`, embedded with `go:embed`; vanilla, no build step).
- `internal/tui/webboard.go`: the open Board hosts the server, started on
  demand (`w` with no task highlighted, the palette entry "Open the
  Board in the browser", `/marshal web`) and stopped by `Workspace.Close`.
  Frames are built by `webBoardFrame` (`boardV2()` plus the screen choice
  `paintV2` makes) on the server's goroutines, never on the input loop and
  never with `w.mu` held. It calls no paint function.

Endpoints, all `GET`:

| Path | What |
|---|---|
| `/` (the link is `/?t=TOKEN`) | The page. Static, no Board data, no token needed. `app.js` reads `t` from the address, replaces the address with `/` (`history.replaceState`, so the token leaves the address bar and the history), and keeps the token in `sessionStorage` for this tab (in memory when storage is unavailable), so a reload works. |
| `/app.js`, `/app.css` | The page's script and style; no token needed. |
| `/api/board` | The current frame as JSON. Needs `Authorization: Bearer TOKEN`. |
| `/api/events` | Server-Sent Events: `event: board` with the frame as one `data:` line whenever what it shows changed (a clock tick alone is no change), at most every 250 ms, a `: heartbeat` comment every 15 s. Frames are built only while a stream is open. Needs the bearer header too, so the page reads it with `fetch()` and a `ReadableStream` (EventSource cannot send headers) and parses the events itself; a stream silent for 45 s counts as dead. The page reconnects with backoff (1 s doubling to 30 s) and shows "disconnected" with the time of the data it still shows; a `401` says the link expired. |

Security rules (tests in `internal/webboard/server_test.go`):

- Binds `127.0.0.1` on a random port; any other address is refused at start.
- A fresh 256-bit token per server (URL-safe base64), compared in constant
  time. `/api/*` takes it only as `Authorization: Bearer TOKEN`: not in the
  query, not as a cookie; without it, `401`. No response ever sets a cookie,
  so no other page or local service on this computer can ride on one (cookies
  ignore the port), and the server needs no `Secure` cookie on plain http.
- The `Host` header must be exactly `127.0.0.1:PORT` or `localhost:PORT`
  (DNS rebinding); anything else is `421`.
- Every method but `GET` is `405` with `Allow: GET`.
- Every response: `Content-Security-Policy: default-src 'self'; script-src
  'self'; style-src 'self'; img-src 'self'; connect-src 'self'; object-src
  'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`,
  `X-Frame-Options: DENY`, `Cache-Control: no-store`, `X-Content-Type-Options:
  nosniff`, `Referrer-Policy: no-referrer`. No CORS headers. No inline script
  or style; bar widths are set through the CSSOM.
- Nothing from the internet: no CDN, fonts or analytics.
- Only what the terminal Board shows. The wire structs are separate from
  `vm`; `TestFrameMapsEveryField` makes every new `vm` field a decision. Never
  sent: `Decision.Link` (the architecture review URL carries its token, which
  is the owner's Accept), and the collectors' `Ref`/`Refs` ids. The page
  writes every value with `textContent`; task titles and Activity text are
  written by workers and models and are never HTML.
- The tokened link goes to the footer only, never to Activity (which the Web
  Board itself shows) or to the Marshal.

## Phase 2: owner actions

The owner answers a decision, accepts, or chooses on a paused card from the
page. Design, not built:

- **Transport**: `POST /api/act` with a JSON body `{"action": "...", "card":
  "<card id>", "csrf": "<token>"}`. Only listed actions: the keys the card
  shows (`Decision.Actions`, `Paused.Actions`, `Ready.Actions`), each mapped
  server-side to the same handler the terminal key reaches
  (`handleV2CardKey`, `handleV2PauseKey`, the inbox decide path), run on the
  Board's own goroutine as a key would be. No free-form commands, no shell.
- **Authentication**: the bearer token as in phase 1 (`Authorization:
  Bearer TOKEN`, no cookie), plus:
  - a per-action CSRF token: the frame carries, per card, a random token bound
    to the card's identity and revision (`HMAC(server key, card id ||
    revision || action)`); a POST must echo it, so a stale page cannot act on
    a newer card, and one token works for one card revision only;
  - an `Origin` check: `Origin` must be exactly `http://127.0.0.1:PORT` or
    `http://localhost:PORT` (absent `Origin` is refused for POST);
  - `Content-Type: application/json` only (no form posts), body at most
    16 KiB, `Sec-Fetch-Site: same-origin` when the browser sends it.
  A bearer header is never sent by the browser on its own, so a page on
  another local port cannot make an authenticated request; the Origin and
  content-type checks are a second line should the token ever reach one.
- **Authority**: the same rules as the browser architecture Accept
  (`internal/tui/architecture_browser_accept*.go`, the store's
  edition-gated operator-instruction receipt). An action from the Web Board is
  the owner's because only the owner holds the link and its token; the
  Marshal never gets either: the link never reaches Activity, the Marshal
  chat, MCP tools or any file the Marshal can read, and the server refuses a
  request from the Marshal's own processes the same way the architecture
  review does. The Marshal can never act as the owner: a self-approval stays
  refused and is shown on the card as it is in the terminal.
- **Confirmation**: a decision that needs two presses in the terminal (arm,
  then Enter) needs two clicks on the page; the hard gate's typed canonical id
  is typed on the page too.
- **Audit**: each action is recorded as the terminal key's would be, with
  "from the Web Board" in the Activity line (without the link).
- **Tests**: POST without or with a wrong bearer token, without or with a wrong/stale CSRF token,
  with a foreign or missing Origin, with a form content type, for an action
  the card does not show, after the card changed; and that a key and a click
  produce the same receipt.

## Phase 3 idea: the Marshal chat in the browser

Show the Marshal chat pane in the page through a terminal emulator (for
example xterm.js, embedded, not from a CDN) attached read-write to the chat's
tmux pane over a WebSocket on the same origin, with the phase 2 rules (bearer
token, Origin, per-connection token). This puts the prompt and transcript on the
page, so it is a separate opt-in from the read-only Board, off by default,
and never available from the phase 1 link alone.
