// Web Board page smoke: drives Playwright's Chromium headless shell over the
// DevTools protocol (no npm packages) against the Web Board demo, whose fake
// action sink answers every action.
//
//   MARSHAL_WEBBOARD_DEMO=paused MARSHAL_WEBBOARD_DEMO_ADDR=127.0.0.1:8771 \
//     go test ./internal/webboard -run TestDemo -v -timeout 0 &
//   node tools/e2e/webboard_smoke.mjs http://127.0.0.1:8771/?t=demo OUTDIR [running-url]
//
// It opens the paused card, checks the big button and the "Tell the
// Marshal" box, presses the button and waits for "Done", sends a message and
// waits for "Sent", and writes screenshots (paused, phone width, after the
// action; and the running screen when its URL is given). Exit status 0 is a
// pass. MARSHAL_HEADLESS_SHELL names the browser when it is not in the
// Playwright cache.
import { spawn } from "node:child_process";
import { readdirSync, writeFileSync, mkdirSync, mkdtempSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";

const [url, out = ".", runningURL] = process.argv.slice(2);
if (!url) {
  console.error("usage: webboard_smoke.mjs PAUSED-URL [OUTDIR] [RUNNING-URL]");
  process.exit(2);
}
mkdirSync(out, { recursive: true });

function shell() {
  if (process.env.MARSHAL_HEADLESS_SHELL) return process.env.MARSHAL_HEADLESS_SHELL;
  const cache = join(homedir(), "Library/Caches/ms-playwright");
  for (const d of readdirSync(cache)) {
    if (d.startsWith("chromium_headless_shell")) return join(cache, d, "chrome-mac/headless_shell");
  }
  throw new Error("no headless_shell in " + cache);
}

const port = 9300 + Math.floor(Math.random() * 500);
const profile = mkdtempSync(join(tmpdir(), "wb-smoke-"));
const browser = spawn(shell(), [`--remote-debugging-port=${port}`, `--user-data-dir=${profile}`, "--no-first-run", "about:blank"], { stdio: "ignore" });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function target() {
  for (let i = 0; i < 50; i++) {
    try {
      const list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
      const page = list.find((t) => t.type === "page");
      if (page) return page.webSocketDebuggerUrl;
    } catch { /* not up yet */ }
    await sleep(100);
  }
  throw new Error("the browser did not start");
}

const ws = new WebSocket(await target());
await new Promise((r) => ws.addEventListener("open", r, { once: true }));
let id = 0;
const waiting = new Map();
ws.addEventListener("message", (e) => {
  const m = JSON.parse(e.data);
  if (m.id && waiting.has(m.id)) {
    const { ok, no } = waiting.get(m.id);
    waiting.delete(m.id);
    m.error ? no(new Error(JSON.stringify(m.error))) : ok(m.result);
  }
});
const send = (method, params = {}) => new Promise((ok, no) => { const n = ++id; waiting.set(n, { ok, no }); ws.send(JSON.stringify({ id: n, method, params })); });
async function evaluate(expr) {
  const r = await send("Runtime.evaluate", { expression: expr, awaitPromise: true, returnByValue: true });
  if (r.exceptionDetails) throw new Error(expr + ": " + JSON.stringify(r.exceptionDetails));
  return r.result.value;
}
async function until(expr, what, ms = 8000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (await evaluate(expr)) return;
    await sleep(100);
  }
  throw new Error("timed out waiting for " + what);
}
async function size(width, height, mobile = false) {
  await send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: mobile ? 2 : 1, mobile });
}
async function shot(name) {
  const r = await send("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
  writeFileSync(join(out, name), Buffer.from(r.data, "base64"));
  console.log("screenshot", join(out, name));
}
async function open(u) {
  await send("Page.navigate", { url: u });
  await until(`document.readyState === "complete" && (document.getElementById("t-live") || {}).textContent === "live"`, "the page to go live");
}

let failed = false;
const check = (ok, what) => { console.log((ok ? "ok   " : "FAIL ") + what); if (!ok) failed = true; };

try {
  await send("Page.enable");
  await send("Runtime.enable");
  // The Board's own palette is dark.
  await send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: "dark" }] });
  if (runningURL) {
    await size(1280, 900);
    await open(runningURL);
    check(await evaluate(`document.getElementById("card").hidden`), "running: no card");
    check(await evaluate(`document.getElementById("now").textContent.startsWith("The Marshal:")`), "running: one sentence of what is happening");
    await shot("20-running.png");
    // With no card: Retry <task> (the Board's r) and Stop all workers
    // (Ctrl+X), each two clicks.
    const controls = await evaluate(`Array.from(document.querySelectorAll("#controls button")).map(b => b.textContent)`);
    check(JSON.stringify(controls) === JSON.stringify(["Retry T12", "Stop all workers"]), `running: controls ${JSON.stringify(controls)}`);
    await evaluate(`document.querySelector("#controls button").click()`);
    await sleep(200);
    check(await evaluate(`document.querySelector("#controls button").textContent === "Click again to retry T12"`), "retry: the first click arms it");
    await evaluate(`document.querySelector("#controls button").click()`);
    await until(`/^Done/.test(document.getElementById("feedback").textContent)`, "Done after Retry");
    check(true, "retry: " + (await evaluate(`document.getElementById("feedback").textContent`)));
    await until(`Array.from(document.querySelectorAll("#controls button")).map(b => b.textContent).join() === "Stop all workers"`, "the retry button to go");
    await shot("20-running-retried.png");
  }
  await size(1280, 900);
  await open(url);
  await until(`!document.getElementById("primary").hidden && document.getElementById("primary").textContent !== ""`, "the primary button");
  const label = await evaluate(`document.getElementById("primary").textContent`);
  check(label === "Move to claude", `paused: primary button "${label}"`);
  const secondary = await evaluate(`Array.from(document.querySelectorAll("#secondary button")).map(b => b.textContent)`);
  check(secondary.length <= 2 && secondary.includes("Stop the run"), `paused: secondary ${JSON.stringify(secondary)}`);
  check(await evaluate(`document.getElementById("say-text").placeholder === "Tell the Marshal…"`), "the Tell the Marshal box");
  await shot("20-paused.png");

  // A phone opens the page at its own width: the details start closed.
  await size(390, 844, true);
  await open(url);
  await until(`document.getElementById("primary").textContent !== ""`, "the primary button at phone width");
  check(await evaluate(`document.documentElement.scrollWidth <= window.innerWidth + 1`), "phone: no sideways scroll");
  check(await evaluate(`document.querySelectorAll("details.pane[open]").length === 0`), "phone: every Details section is closed");
  await shot("20-phone.png");
  await size(1280, 900);
  await open(url);
  await until(`document.getElementById("primary").textContent !== ""`, "the primary button");

  // Stop takes two clicks: the first only arms it.
  await evaluate(`Array.from(document.querySelectorAll("#secondary button")).find(b => b.textContent === "Stop the run").click()`);
  await sleep(200);
  check(await evaluate(`Array.from(document.querySelectorAll("#secondary button")).some(b => b.textContent === "Click again to stop the run")`), "stop: the first click arms it");
  check(await evaluate(`!document.getElementById("card").hidden`), "stop: nothing was sent on the first click");

  await evaluate(`document.getElementById("primary").click()`);
  await until(`/^Done/.test(document.getElementById("feedback").textContent)`, "Done after the primary button");
  check(true, "primary: " + (await evaluate(`document.getElementById("feedback").textContent`)));
  await until(`document.getElementById("card").hidden`, "the card to go after the action");
  check(await evaluate(`document.getElementById("a-rows").textContent.includes("From the web: move to claude")`), "activity: the web line");
  await shot("20-after-action.png");
  await evaluate(`document.getElementById("say-text").value = "Please also run the review tests."`);
  await evaluate(`document.getElementById("say-send").click()`);
  await until(`document.getElementById("feedback").textContent.startsWith("Sent")`, "Sent after Send");
  check(await evaluate(`document.getElementById("say-text").value === ""`), "say: the box is cleared");
} catch (e) {
  console.error("FAIL", e.message);
  failed = true;
} finally {
  ws.close();
  browser.kill();
}
process.exit(failed ? 1 : 0);
