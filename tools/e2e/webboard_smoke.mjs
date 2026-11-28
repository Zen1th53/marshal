// Web Board page smoke: drives Playwright's Chromium headless shell over the
// DevTools protocol (no npm packages) against the Web Board demos, whose
// fake action sink answers every action.
//
//   node tools/e2e/webboard_smoke.mjs OUTDIR --demo [--views-only]
//
// starts one demo per fixture (running, paused, ready, planning, decision,
// finished, bare)
// with `go test ./internal/webboard -run TestDemo` on ports 8771-8777, or
// give the URLs of demos already running:
//
//   node tools/e2e/webboard_smoke.mjs OUTDIR running=http://127.0.0.1:8771/?t=demo paused=…
//
// It visits every section route (#home, #work, #work/<task>, #architecture,
// #team, #activity, #settings) of every fixture at 1440 and 390 px wide and
// checks: no sideways scroll, the nav and the From-you pill are there, the
// section is shown and not blank, no console error, no exception and no
// Content-Security-Policy violation. Then, last (an action changes the demo),
// it runs the action flows: Retry from the Work controls and from the task
// drawer (two clicks each), the architecture map, the paused card's primary
// button and the Tell-the-Marshal box. Screenshots go to
// OUTDIR/30-<fixture>-<route>-<width>.png. Exit status 0 is a pass.
// MARSHAL_HEADLESS_SHELL names the browser when it is not in the Playwright
// cache. --views-only runs the route, theme, contrast, focus and motion sweep
// without the action flows that change fixture state.
import { createRequire } from "node:module";
import { spawn } from "node:child_process";
import { readdirSync, writeFileSync, mkdirSync, mkdtempSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const architectureFixture = require("../../internal/webboard/testdata/architecture-fixture.cjs");
const args = process.argv.slice(2);
const out = args[0];
if (!out || args.length < 2) {
  console.error("usage: webboard_smoke.mjs OUTDIR --demo | fixture=URL ...");
  process.exit(2);
}
mkdirSync(out, { recursive: true });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const fixtures = ["running", "paused", "ready", "planning", "decision", "finished", "bare"];
const urls = {};
const demos = [];
if (args.includes("--demo")) {
  const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
  fixtures.forEach((name, i) => {
    const addr = `127.0.0.1:${8771 + i}`;
    urls[name] = `http://${addr}/?t=demo`;
    demos.push(spawn("go", ["test", "./internal/webboard", "-run", "^TestDemo$", "-count=1", "-timeout", "0"], {
      cwd: root, stdio: "ignore", detached: true,
      env: { ...process.env, MARSHAL_WEBBOARD_DEMO: name, MARSHAL_WEBBOARD_DEMO_ADDR: addr },
    }));
  });
} else {
  for (const a of args.slice(1)) {
    const [k, v] = a.split(/=(.*)/s);
    if (v) urls[k] = v;
  }
}
async function up(u) {
  for (let i = 0; i < 600; i++) {
    try { if ((await fetch(new URL("/app.js", u))).ok) return; } catch { /* not yet */ }
    await sleep(100);
  }
  throw new Error("the demo did not start: " + u);
}
function stopDemos() {
  for (const d of demos) { try { process.kill(-d.pid, "SIGTERM"); } catch { /* gone */ } }
}

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

let failed = false;
const check = (ok, what) => { console.log((ok ? "ok   " : "FAIL ") + what); if (!ok) failed = true; };

// problems are the page's console errors, exceptions and CSP violations
// since the last look.
let problems = [];
let ws, id = 0;
const waiting = new Map();
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
async function size(width) {
  const mobile = width < 720;
  await send("Emulation.setDeviceMetricsOverride", { width, height: mobile ? 844 : 900, deviceScaleFactor: 1, mobile });
}
async function shot(name) {
  const r = await send("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
  writeFileSync(join(out, name), Buffer.from(r.data, "base64"));
  console.log("screenshot", join(out, name));
}
async function open(u) {
  await send("Page.navigate", { url: u });
  await until(`document.readyState === "complete" && (document.getElementById("t-live") || {}).textContent === "live"`, "the page to go live");
  await sleep(150);
}
const visible = (sel) => `(function(){var e=document.querySelector(${JSON.stringify(sel)});if(!e)return false;var r=e.getBoundingClientRect();return r.width>0&&r.height>0&&getComputedStyle(e).visibility!=="hidden";})()`;
async function look(label) {
  await sleep(100);
  const csp = await evaluate(`(window.__csp || []).splice(0)`);
  const all = problems.splice(0).concat(csp.map((c) => "CSP: " + c));
  check(all.length === 0, `${label}: no console error, exception or CSP violation${all.length ? " " + JSON.stringify(all) : ""}`);
}

// Composite translucent surfaces before checking HTML and diagram text.
function textContrast() {
  const rgba = (s) => (s.match(/[\d.]+/g) || []).map(Number);
  const over = (a, b) => a.slice(0, 3).map((v, i) => v * (a[3] ?? 1) + b[i] * (1 - (a[3] ?? 1)));
  const background = (e) => {
    const c = rgba(getComputedStyle(e).backgroundColor);
    return over(c, e.parentElement ? background(e.parentElement) : [255, 255, 255]);
  };
  const luminance = (c) => c.reduce((sum, v, i) => {
    v /= 255; return sum + (v <= .04045 ? v / 12.92 : ((v + .055) / 1.055) ** 2.4) * [.2126, .7152, .0722][i];
  }, 0);
  return [...document.querySelectorAll("body *")].filter((e) => {
    const s = getComputedStyle(e), r = e.getBoundingClientRect();
    return (e instanceof HTMLElement || e.tagName === "text") && !e.closest(".sr") && r.width > 1 && r.height > 1 && s.visibility !== "hidden" &&
      [...e.childNodes].some((n) => n.nodeType === 3 && n.textContent.trim());
  }).flatMap((e) => {
    let bg = background(e);
    if (e.tagName === "text") {
      const point = e.getBoundingClientRect(), x = point.left + point.width / 2, y = point.top + point.height / 2;
      for (const shape of e.ownerSVGElement.querySelectorAll("rect, circle")) {
        const box = shape.getBoundingClientRect(), fill = getComputedStyle(shape).fill;
        if (shape.compareDocumentPosition(e) & Node.DOCUMENT_POSITION_FOLLOWING &&
            x >= box.left && x <= box.right && y >= box.top && y <= box.bottom && fill !== "none") {
          bg = over(rgba(fill), bg);
        }
      }
    }
    const fg = over(rgba(getComputedStyle(e)[e.tagName === "text" ? "fill" : "color"]), bg);
    const a = luminance(fg), b = luminance(bg), ratio = (Math.max(a, b) + .05) / (Math.min(a, b) + .05);
    return ratio >= 4.5 ? [] : [{ text: e.textContent.slice(0, 55), ratio: +ratio.toFixed(2) }];
  });
}

const routes = { running: ["home", "work", "work/T04", "architecture", "team", "activity", "settings"] };
const plain = ["home", "work", "architecture", "team", "activity", "settings"];

try {
  for (const name of fixtures) if (urls[name]) await up(urls[name]);
  ws = new WebSocket(await target());
  await new Promise((r) => ws.addEventListener("open", r, { once: true }));
  ws.addEventListener("message", (e) => {
    const m = JSON.parse(e.data);
    if (m.id && waiting.has(m.id)) {
      const { ok, no } = waiting.get(m.id);
      waiting.delete(m.id);
      m.error ? no(new Error(JSON.stringify(m.error))) : ok(m.result);
      return;
    }
    // The demo has no accepted contract; exercise the existing workspace fixture.
    if (m.method === "Fetch.requestPaused") {
      send("Fetch.fulfillRequest", { requestId: m.params.requestId, responseCode: 200,
        responseHeaders: [{ name: "Content-Type", value: "application/json" }],
        body: Buffer.from(JSON.stringify(architectureFixture)).toString("base64") }).catch((e) => problems.push(e.message));
      return;
    }
    if (m.method === "Runtime.exceptionThrown") problems.push("exception: " + (m.params.exceptionDetails.exception || {}).description);
    if (m.method === "Runtime.consoleAPICalled" && (m.params.type === "error" || m.params.type === "assert")) problems.push("console: " + JSON.stringify(m.params.args.map((a) => a.value)));
    if (m.method === "Log.entryAdded" && m.params.entry.level === "error") problems.push("log: " + m.params.entry.text);
  });
  if (args.includes("--demo")) await send("Fetch.enable", { patterns: [{ urlPattern: "*/api/architecture", requestStage: "Request" }] });
  await send("Page.enable");
  await send("Runtime.enable");
  await send("Log.enable");
  await send("Page.addScriptToEvaluateOnNewDocument", {
    source: `window.__csp = []; document.addEventListener("securitypolicyviolation", function (e) { window.__csp.push(e.violatedDirective + " " + (e.blockedURI || "inline")); });`,
  });
  await send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: "dark" }] });

  // 1. Every route of every fixture at both widths.
  for (const name of fixtures) {
    if (!urls[name]) continue;
    for (const width of [1440, 390]) {
      await size(width);
      await open(urls[name]);
      for (const r of routes[name] || plain) {
        await evaluate(`location.hash = ${JSON.stringify("#" + r)}`);
        await sleep(200);
        const sect = r.split("/")[0];
        const label = `${name} #${r} @${width}`;
        check(await evaluate(`document.documentElement.scrollWidth <= window.innerWidth + 1`), `${label}: no sideways scroll`);
        check(await evaluate(`Array.from(document.querySelectorAll("nav.side .navlist a.nav")).filter(a => a.getBoundingClientRect().width > 0).length === 5`), `${label}: the five sections in the nav`);
        check(await evaluate(visible("#you-pill")) && await evaluate(`document.getElementById("you").textContent.trim() !== ""`), `${label}: the From-you pill`);
        check(await evaluate(`!document.getElementById("s-${sect}").hidden && document.getElementById("s-${sect}").innerText.trim().length > 20`), `${label}: the section is shown and not blank`);
        check(await evaluate(`document.querySelector('a.nav[data-route="${sect}"]').getAttribute("aria-current") === "page"`), `${label}: the nav marks the section`);
        check(await evaluate(`!/read-only · act/.test(document.body.innerText)`), `${label}: no "read-only" footer`);
        if (r.startsWith("work/")) {
          check(await evaluate(`!document.getElementById("drawer").hidden && document.getElementById("dr-title").textContent.startsWith("${r.slice(5)}")`), `${label}: the task drawer`);
          check(await evaluate(`(function(){var b=document.getElementById("drawer").getBoundingClientRect();return b.top >= 0 && b.top < innerHeight - 120;})()`), `${label}: the drawer is on screen`);
        }
        if (r === "architecture" && args.includes("--demo")) {
          await until(`document.querySelectorAll(".aw-scroll svg").length === 3`, "the architecture fixture");
        }
        await look(label);
        await shot(`30-${name}-${r.replace("/", "-")}-${width}.png`);
        if (name === "running" && !r.includes("/")) {
          for (const theme of ["light", "dark"]) {
            await evaluate(`document.documentElement.setAttribute("data-theme", ${JSON.stringify(theme)})`);
            check(await evaluate(`document.documentElement.scrollWidth <= innerWidth + 1`), `${label}: ${theme} theme fits`);
            const contrast = await evaluate(`(${textContrast.toString()})()`);
            check(contrast.length === 0, `${label}: ${theme} text contrast >= 4.5:1 ${JSON.stringify(contrast)}`);
          }
          await send("Input.dispatchKeyEvent", { type: "keyDown", key: "Tab", code: "Tab", windowsVirtualKeyCode: 9 });
          await send("Input.dispatchKeyEvent", { type: "keyUp", key: "Tab", code: "Tab", windowsVirtualKeyCode: 9 });
          await evaluate(`document.querySelector("#n-${sect}").focus()`);
          check(await evaluate(`getComputedStyle(document.activeElement).outlineStyle !== "none"`), `${label}: keyboard focus visible`);
          await evaluate(`document.activeElement.blur()`);
          await send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }] });
          check(await evaluate(`getComputedStyle(document.querySelector("#home-bar > span")).transitionDuration === "0s"`), `${label}: reduced motion`);
          await send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: "dark" }] });
        }
      }
    }
  }

  if (!args.includes("--views-only")) {
    // The checks can fail: an inline style set by script is a CSP violation
    // the sweep would have caught.
    if (urls.running) {
      await size(1440);
      await open(urls.running);
      await evaluate(`document.body.setAttribute("style", "color: red")`);
      await sleep(200);
      const caught = await evaluate(`(window.__csp || []).splice(0)`);
      problems.splice(0);
      check(caught.length > 0, `self-test: an inline style is reported as a CSP violation ${JSON.stringify(caught)}`);
      await evaluate(`document.body.removeAttribute("style")`);
    }

    // Finished, and a decision the page does not have the facts for.
    if (urls.finished) {
      await size(1440);
      await open(urls.finished);
      const box = await evaluate(`document.getElementById("home-empty").hidden ? "" : document.getElementById("home-empty").innerText`);
      check(/^Finished: review-fixes/.test(box) && /11 tasks merged, 1 cancelled/.test(box) && /What next\?/.test(box), `finished: ${JSON.stringify(box)}`);
      check(await evaluate(`document.querySelectorAll("#controls button").length === 0`), "finished: no Retry or Stop");
    }
    if (urls.bare) {
      await size(1440);
      await open(urls.bare);
      check(await evaluate(`!document.getElementById("card").hidden && document.getElementById("primary").hidden && /Decide this in the terminal Board/.test(document.getElementById("secondary").textContent) && Array.from(document.querySelectorAll("#card button")).filter(b => b.getBoundingClientRect().width > 0).length === 0`),
        "bare decision: not offered on the web, said so");
    }

    // 2. Deep link and Back.
    if (urls.running) {
      await size(1440);
      await open(urls.running.replace(/#.*$/, "") + "#team");
      check(await evaluate(`!document.getElementById("s-team").hidden && location.search === "" && location.hash === "#team"`), "deep link: #team survives the token being taken out of the address");
      await evaluate(`location.hash = "#work"`);
      await sleep(150);
      await evaluate(`history.back()`);
      await sleep(300);
      check(await evaluate(`!document.getElementById("s-team").hidden`), "Back returns to the section before");
      const team = await evaluate(`document.getElementById("team-cards").innerText`);
      check(/not reported by the CLI/.test(team) && /resets/.test(team) && /read /.test(team), "team: every limit row, the unreported one in words, reset and reading age");
      check(/opus · weekly window/.test(team) && /Gemini 3\.8 · 5-hour window/.test(team) && /all models/.test(team) && /decides dispatch/.test(team) && /Usable/.test(team),
        "team: per-family limit rows from the limits contract, the deciding row marked, the dispatch state");
      const act = await evaluate(`document.getElementById("act-groups").innerText`);
      check(/The Marshal asked for project contract/.test(act) && !/decision-[0-9a-f]{6}/.test(act), "activity: decision ids folded into plain words");
      await evaluate(`location.hash = "#settings"`);
      await sleep(200);
      const set = await evaluate(`document.getElementById("set-facts").innerText`);
      check(/v0\.3\.9 is available/.test(set) && /64% CPU/.test(set) && /lane cap 6/.test(set) && /MCP\s+working/.test(set) && /tasks: Marshal, then you/.test(set) && /telemetry\)\s+on/.test(set), "settings: update, host load, thermal cap, MCP, approval, telemetry");
      await look("deep links");

      // The Marshal's last chat turn, and its question in the pill.
      await evaluate(`location.hash = "#home"`);
      await sleep(200);
      check(await evaluate(`/Should I retry it from scratch/.test(document.getElementById("says-text").textContent) && document.getElementById("says-empty").hidden`), "home: The Marshal says");
      check(await evaluate(`document.getElementById("you").textContent === "the Marshal asked you something in chat" && document.getElementById("you-pill").classList.contains("act")`), "pill: the Marshal asked you something in chat (nowLine.chatAsked)");

      // Security, Verify and Memory: listed, dimmed, not links, no route.
      for (const width of [1440, 390]) {
        await size(width);
        await evaluate(`location.hash = "#settings"`);
        await sleep(200);
        const soon = await evaluate(`Array.from(document.querySelectorAll(".soon")).filter(e => e.getBoundingClientRect().width > 0).map(e => ({
          t: e.textContent, dis: e.getAttribute("aria-disabled"), title: e.title, tab: e.tabIndex, inner: e.querySelectorAll("a,button,[tabindex]").length, tag: e.tagName }))`);
        check(soon.length === 3 && soon.every((s) => s.dis === "true" && s.title === "Coming in a later release" && s.tab < 0 && s.inner === 0 && s.tag === "DIV")
          && ["Security", "Verify", "Memory"].every((n, i) => soon[i].t.startsWith(n) && soon[i].t.endsWith("Coming soon")), `@${width}: Security, Verify, Memory are shown as coming soon, not links ${JSON.stringify(soon)}`);
        await evaluate(`Array.from(document.querySelectorAll(".soon")).filter(e => e.getBoundingClientRect().width > 0).forEach(e => e.click())`);
        await sleep(150);
        check(await evaluate(`location.hash === "#settings" && !document.getElementById("s-settings").hidden`), `@${width}: clicking them goes nowhere`);
        check(await evaluate(`Array.from(document.querySelectorAll(".navlist a.nav")).length === 5`), `@${width}: still five sections in the bar`);
      }
      await size(1440);
    }

    // 3. Action flows, last: each changes its demo.
    if (urls.running) {
      await size(1440);
      await open(urls.running + "#work");
      const controls = await evaluate(`Array.from(document.querySelectorAll("#controls button")).map(b => b.textContent)`);
      check(JSON.stringify(controls) === JSON.stringify(["Retry T12 from scratch", "Stop all workers"]), `work: controls ${JSON.stringify(controls)}`);
      check(await evaluate(`!document.querySelector("#n-work .dot").hidden`), "work: the nav dot says the owner can act here");
      await evaluate(`location.hash = "#work/T12"`);
      await until(`document.querySelectorAll("#dr-acts button").length === 1`, "the drawer's Retry");
      await evaluate(`document.querySelector("#dr-acts button").click()`);
      await sleep(200);
      check(await evaluate(`document.querySelector("#dr-acts button").textContent === "Click again to retry T12 from scratch"`), "drawer retry: the first click only arms it");
      await evaluate(`document.querySelector("#dr-acts button").click()`);
      await until(`/^Done/.test(document.getElementById("feedback").textContent)`, "Done after Retry");
      check(true, "drawer retry: " + (await evaluate(`document.getElementById("feedback").textContent`)));
      await until(`Array.from(document.querySelectorAll("#controls button")).map(b => b.textContent).join() === "Stop all workers"`, "the retry button to go");
      await look("retry flow");
      await shot("30-running-after-retry-1440.png");
    }
    if (urls.decision) {
      await size(1440);
      await open(urls.decision + "#architecture");
      await until(`Array.from(document.querySelectorAll("#ar-acts button")).some(b => b.textContent === "Open the architecture map")`, "the architecture map button");
      await evaluate(`document.querySelector("#ar-acts button").click()`);
      await until(`!document.getElementById("review").hidden`, "the review link");
      check(await evaluate(`document.getElementById("review-link").target === "_blank" && document.querySelectorAll("iframe").length === 0`), "architecture: the review opens in its own tab, never embedded");
      await look("architecture flow");
    }
    if (urls.paused) {
      await size(390);
      await open(urls.paused);
      check(await evaluate(`document.getElementById("you-pill").classList.contains("act") && document.getElementById("you-pill").getAttribute("href") === "#home"`), "paused: the pill is amber and leads to the card");
      await until(`!document.getElementById("primary").hidden && document.getElementById("primary").textContent !== ""`, "the primary button");
      const label = await evaluate(`document.getElementById("primary").textContent`);
      check(label === "Move to claude", `paused: primary button "${label}"`);
      const secondary = await evaluate(`Array.from(document.querySelectorAll("#secondary button")).map(b => b.textContent)`);
      check(secondary.length <= 2 && secondary.includes("Stop the run"), `paused: secondary ${JSON.stringify(secondary)}`);
      await evaluate(`Array.from(document.querySelectorAll("#secondary button")).find(b => b.textContent === "Stop the run").click()`);
      await sleep(200);
      check(await evaluate(`Array.from(document.querySelectorAll("#secondary button")).some(b => b.textContent === "Click again to stop the run")`), "stop: the first click arms it");
      await evaluate(`document.getElementById("primary").click()`);
      await until(`/^Done/.test(document.getElementById("feedback").textContent)`, "Done after the primary button");
      check(true, "primary: " + (await evaluate(`document.getElementById("feedback").textContent`)));
      await until(`document.getElementById("card").hidden`, "the card to go after the action");
      check(await evaluate(`document.getElementById("latest").textContent.includes("From the web: move to claude")`), "home: the web line in Latest");
      await evaluate(`document.getElementById("say-text").value = "Please also run the review tests."`);
      await evaluate(`document.getElementById("say-send").click()`);
      await until(`document.getElementById("feedback").textContent.startsWith("Sent")`, "Sent after Send");
      check(await evaluate(`document.getElementById("say-text").value === ""`), "say: the box is cleared");
      await look("paused flow");
      await shot("30-paused-after-action-390.png");
    }
  }
} catch (e) {
  console.error("FAIL", e.message);
  failed = true;
} finally {
  try { ws && ws.close(); } catch { /* closed */ }
  browser.kill();
  stopDemos();
}
process.exit(failed ? 1 : 0);
