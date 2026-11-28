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

## Phase 2: owner actions (built)

The owner presses the buttons of the card on screen and writes to the Marshal
from the page. What shipped, and where it differs from the first design:

- **Page**: one sentence of what is happening (`nowLine`), the "From you"
  line, and, when a card waits, its title, cause, what the main button will
  do, one big primary button (the card's first action the web can take, its
  label capitalized) and at most two secondary text buttons; a single
  **Tell the Marshal…** box with **Send**. Everything else is under
  `<details>` sections, closed by default below 861 px. After each action the
  page says **Done** / **Sent** / **Queued** with the Board's own answer, or
  **Refused** with the plain reason, then reads the frame again. Keys that only
  change the terminal's own screen are not offered: `c` (terminal clipboard),
  `7` (a terminal view), `e` (edit in the terminal), `q` (quit), `esc`
  ("later"), and the inbox card's `⏎ details`; `k` (comment) focuses the
  message box.
- **Transport**: `POST /api/act` accepts named `decide` choices, `cmd`
  envelopes, and `say` messages. `action: "key"` returns `400` with a request
  to refresh and use a named action. The page never sends terminal keys.
- **Checks**: Host, bearer, same Origin, JSON content type, bounded body,
  current card and single-use action token are checked before dispatch.
  Tokens bind the card, choice and action count. Spending a token before
  dispatch prevents replay across tabs. Card actions also check their revision
  on the input loop, so queued actions cannot affect a replacement card.
- **Card action inventory**: the former key route could reach these actions:

  | Card | Controls | Named actions |
  | --- | --- | --- |
  | Paused | Enter, w, d, x | `pause/continue`, `pause/wait`, `pause/details`, `pause/stop` |
  | Architecture | a, k | `architecture.open`, local comment composer |
  | Plan | Enter, 7, k | `plan.approve`, `plan.read`, local comment composer |
  | Inbox | Enter, a, d, confirmation text | `inbox.inspect`, `inbox.decide`, `inbox.return` |
  | Change | Enter, k, x | `change.apply`, `change.keep`, `change.release` |
  | Requirements | confirm, edit a line | `requirements.confirm`, `requirements.edit` |
  | Question, changed files, next step | answer or button reference | `card.choose` |
  | Decision cancellation | Esc | `card/dismiss` |

  Copying a link, quitting, and terminal-only view controls stay local.
  Screen retry and stop-workers buttons use the existing bound task and run
  commands. The paused card's named operations share `runPauseAction` with
  terminal controls. That handler calls the same retry, move, resume, stop,
  quota timer and detail services, and returns the same answer and refusal
  metadata. A raw error detail remains a successful read. Stop requires a
  second click. Asynchronous commands retain the existing completion wait.
- **Confirmation**: typed plan and inbox choices keep their existing second
  step, digest and canonical-id checks. Cancel uses `card/dismiss`.
- **Authority**: an action is the owner's because only the owner holds the
  link; the link reaches the footer only, never Activity, the Marshal chat,
  MCP tools or a file the Marshal reads. There is no peer-process check (the
  architecture review has none either; it relies on its token in the same
  way). Nothing on the page accepts an architecture: the card offers no
  accept key, and **Open in browser** returns the review's private link in
  the POST answer only (never in a frame, never in Activity), where the owner
  accepts as before. A self-approval stays refused and shown. `q` is refused.
- **Audit**: the handlers write their own Activity lines; the sink adds one
  "From the web: <button> (<card title>)." per accepted action (and "From
  the web: a message to the Marshal (sent|queued).").
- **Tests**: `internal/webboard/act_test.go` (bearer, Origin, Sec-Fetch-Site,
  content type, size, unknown fields, stale and replayed tokens, card
  change, methods, no cookie, Host, read-only server without a sink, fresh
  tokens pushed on the stream); `internal/tui/webboard_act_test.go` (darwin
  and Linux: the paused card's button runs what `Enter` runs, the two-step
  stop, the raw error, refusals, say without a chat, the input loop and its
  timeout) and `webboard_act_linux_test.go` (the quota move, the
  architecture link, the inbox's two steps); `tools/e2e/webboard_smoke.mjs`
  drives Playwright's headless shell over DevTools against the demo, whose
  fake sink answers every action.

## Phase 3 idea: the Marshal chat in the browser

Show the Marshal chat pane in the page through a terminal emulator (for
example xterm.js, embedded, not from a CDN) attached read-write to the chat's
tmux pane over a WebSocket on the same origin, with the phase 2 rules (bearer
token, Origin, per-connection token). This puts the prompt and transcript on the
page, so it is a separate opt-in from the read-only Board, off by default,
and never available from the phase 1 link alone.

## Settings guidance

The Web Settings page and the terminal command `/marshal settings` read the same catalogue, so they show the same keys, units, defaults, edition locks and when a change applies. The generated tables in [configuration](../configuration.md#settings-reference) are produced from that catalogue and checked in CI. The classic Board is removed; the Web page and the terminal Board are the two views of one run.
