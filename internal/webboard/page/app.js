// Marshal Web Board: the terminal Board in a browser. The top says what is
// happening and, when the Board waits for the owner, shows the card's own
// buttons; a click presses that button on the Board (POST /api/act with the
// frame's one-use token). Every value from the frame is written with
// textContent, never as HTML.
"use strict";
(function () {
  var $ = function (id) { return document.getElementById(id); };
  var frame = null, frameAt = 0, lastOK = null;
  var retry = 0, retryTimer = null;

  // The link's token (?t=…) is read once, taken out of the address bar and
  // the history at once, and kept for this tab only, so a reload still
  // works. It goes to the server as a bearer header; no cookie is used.
  var tokenKey = "marshal-board-token";
  var token = (function () {
    var t = null;
    try { t = new URLSearchParams(location.search).get("t"); } catch (e) { t = null; }
    if (t) {
      try { history.replaceState(null, "", "/"); } catch (e) { /* keep going */ }
      try { sessionStorage.setItem(tokenKey, t); } catch (e) { /* in memory only */ }
      return t;
    }
    try { return sessionStorage.getItem(tokenKey); } catch (e) { return null; }
  })();
  function auth() { return { "Authorization": "Bearer " + token }; }

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined && text !== null) e.textContent = String(text);
    return e;
  }
  function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); return node; }
  function show(id, on) { $(id).hidden = !on; }
  function setText(id, text) { $(id).textContent = text === undefined || text === null || text === "" ? "" : String(text); }
  function toneClass(t) { return t ? "tone-" + t : ""; }
  function chip(node, text, tone, solid) {
    node.textContent = text || "";
    node.className = "chip" + (tone ? " " + toneClass(tone) : "") + (solid ? " solid" : "");
  }
  function pad(n) { return n < 10 ? "0" + n : "" + n; }
  function hm(iso) {
    if (!iso) return "";
    var d = new Date(iso);
    return isNaN(d) ? "" : pad(d.getHours()) + ":" + pad(d.getMinutes());
  }
  function dur(s) {
    if (s === undefined || s === null || s < 0) return "–";
    s = Math.floor(s);
    if (s < 60) return s + "s";
    var m = Math.floor(s / 60);
    if (m < 60) return m + "m " + pad(s % 60) + "s";
    return Math.floor(m / 60) + "h " + pad(m % 60) + "m";
  }
  function compact(n) {
    if (!n) return "0";
    if (n >= 1e6) return (n / 1e6).toFixed(1).replace(/\.0$/, "") + "M";
    if (n >= 1e3) return Math.round(n / 1e3) + "k";
    return String(n);
  }
  // since is how long ago the frame came; it stops while disconnected, so
  // the lanes' clocks do not run on data that is no longer live.
  var downAt = 0;
  function since() { return frame ? ((downAt || performance.now()) - frameAt) / 1000 : 0; }
  function bar(id, pct, tone) {
    var b = $(id), known = pct !== undefined && pct !== null && pct >= 0;
    var v = known ? Math.max(0, Math.min(100, pct)) : 0;
    b.firstElementChild.style.width = v + "%";
    b.className = b.className.replace(/\btone-\w+/g, "").trim() + (tone ? " " + toneClass(tone) : "");
    if (known) { b.setAttribute("aria-valuenow", String(v)); b.removeAttribute("aria-valuetext"); }
    else { b.removeAttribute("aria-valuenow"); b.setAttribute("aria-valuetext", "unknown"); }
  }
  function leftTone(p) { return p < 0 ? "" : p <= 15 ? "bad" : p <= 35 ? "warn" : "ok"; }

  function renderTop(f) {
    var t = f.top;
    setText("t-version", t.version);
    setText("t-project", t.project || "–");
    setText("t-run", t.run ? "· " + t.run : "");
    chip($("t-state"), t.state || "–", t.stateTone, true);
    document.title = (t.state ? t.state + " · " : "") + (t.project || "Marshal") + " · Marshal Board";
  }

  function sentence(text) {
    text = String(text || "").trim();
    if (!text) return "";
    text = text.charAt(0).toUpperCase() + text.slice(1);
    return /[.!?…]$/.test(text) ? text : text + ".";
  }

  // renderNow is the one sentence of what is happening and the one line of
  // what the Board needs from the owner.
  function renderNow(f) {
    var n = f.nowLine, parts = [];
    if (n.marshal) parts.push("The Marshal: " + n.marshal);
    if (n.work) parts.push("Work: " + n.work);
    setText("now", parts.length ? parts.map(sentence).join(" ") : "Connected to the Board.");
    setText("you", n.fromYou || "nothing");
    $("you-box").className = "you" + (n.fromYouOK ? "" : " act");
  }

  // Keys that only change the terminal's own screen are not offered here:
  // c copies to the terminal's clipboard, 7 opens a terminal view, e edits
  // in the terminal, q quits the terminal Board, esc steps the card aside.
  var terminalOnly = { "c": 1, "7": 1, "e": 1, "q": 1, "esc": 1 };
  function cap(s) { s = String(s || ""); return s.charAt(0).toUpperCase() + s.slice(1); }

  // cardButtons are the card's buttons as the page offers them: the first
  // is the primary, at most two more are secondary.
  function cardButtons(f) {
    var d = f.decision, p = f.paused, keys = d ? d.actions : p ? p.actions : [];
    var out = [];
    keys.forEach(function (k) {
      if (terminalOnly[k.key]) return;
      if (d && d.kind === "inbox" && k.key === "⏎") return; // details: the terminal's inspector
      out.push({ key: k.key, label: cap(k.label), local: k.key === "k" });
    });
    if (out.length > 3) {
      // Keep the primary and the stop; the raw error is the first to go.
      var first = out[0], rest = out.slice(1).filter(function (b) { return b.key !== "d"; });
      out = [first].concat(rest.slice(0, 2));
    }
    return out;
  }

  var armedStop = null, confirmStep = null;

  // renderControls is the screen's own buttons when no card is shown: Retry
  // <task> while the Board's r would retry one, and Stop all workers
  // (Ctrl+X). Both take a second click before anything is sent.
  var armedControl = null;
  function renderControls(f) {
    var box = clear($("controls"));
    var keys = !f.decision && !f.paused && f.act ? (f.keys || []) : [];
    box.hidden = keys.length === 0;
    keys.forEach(function (k) {
      if (!f.act.tokens[k.key]) return;
      var id = f.act.card + "|" + k.key, armed = armedControl === id;
      var retry = k.key === "r";
      var text = armed ? "Click again to " + k.label : cap(k.label);
      var b = button(retry ? "primary" + (armed ? " danger" : "") : "linkbtn" + (armed ? " danger" : ""), text, function () {
        if (!armed) {
          armedControl = id;
          feedback("wait", retry ? "Retrying starts the task again in a fresh session, keeping its branch. Click again to retry." :
            "This stops every worker; the Marshal keeps running. Click again to stop them.");
          render(frame);
          setTimeout(function () { if (armedControl === id) { armedControl = null; render(frame); } }, 6000);
          return;
        }
        armedControl = null;
        act({ action: "key", key: k.key, confirmed: true }, cap(k.label));
      });
      box.appendChild(b);
    });
  }

  function renderCard(f) {
    var card = $("card"), d = f.decision, p = f.paused;
    var cardID = f.act ? f.act.card : "";
    if (confirmStep && confirmStep.card !== cardID && !pending) confirmStep = null;
    renderControls(f);
    if (!d && !p) { card.hidden = true; $("d-card").hidden = true; confirmStep = null; return; }
    card.hidden = false;
    card.className = "card" + (p && !d ? " paused" : "");
    clear($("card-nodes")); clear($("card-lines")); clear($("card-tasks"));
    var more = false;
    if (d) {
      setText("card-title", d.title);
      $("card-self").hidden = !d.selfApproval;
      setText("card-self", d.selfApproval);
      setText("card-summary", d.summary);
      setText("card-will", d.corner || "");
      setText("card-changed", d.changedNote);
      d.nodes.forEach(function (n) {
        var li = el("li", n.changed ? "changed" : "", n.name);
        if (n.sub) li.appendChild(el("small", "", n.sub));
        if (n.changed) li.appendChild(el("small", "tone-info", "changed by this goal"));
        $("card-nodes").appendChild(li);
      });
      d.lines.forEach(function (l) {
        $("card-lines").appendChild(el("dt", "", l.label || l.key || ""));
        var dd = el("dd", toneClass(l.tone), l.text);
        if (l.note) dd.appendChild(el("span", "dim", " · " + l.note));
        $("card-lines").appendChild(dd);
      });
      setText("card-extra", "");
      more = !!(d.changedNote || d.nodes.length || d.lines.length);
    } else {
      setText("card-title", p.title);
      $("card-self").hidden = true;
      setText("card-summary", [p.cause, p.resets].filter(Boolean).join(" · "));
      setText("card-will", p.will ? "The main button will " + p.will.split("\n")[0] : "");
      setText("card-changed", "");
      p.tasks.forEach(function (t) {
        var li = el("li");
        li.appendChild(el("b", "", t.title));
        li.appendChild(el("span", "dim", " · " + [t.progress, t.kept].filter(Boolean).join(" · ")));
        $("card-tasks").appendChild(li);
      });
      setText("card-extra", [p.will && p.will.split("\n").slice(1).join(" "), p.room, p.run].filter(Boolean).join(" · "));
      more = !!(p.tasks.length || p.room || p.run);
    }
    $("d-card").hidden = !more;
    renderButtons(f);
  }

  function button(cls, text, onClick) {
    var b = el("button", cls, text);
    b.type = "button";
    b.disabled = pending;
    b.addEventListener("click", onClick);
    return b;
  }

  // renderButtons draws the primary and the secondary buttons, or the
  // confirmation the Board waits for after a first press.
  function renderButtons(f) {
    var prim = $("primary"), sec = clear($("secondary")), typed = $("confirm-text");
    var acts = !!f.act;
    prim.disabled = pending || !acts;
    var fresh = prim.cloneNode(false);
    prim.parentNode.replaceChild(fresh, prim);
    prim = fresh;
    prim.className = "primary";
    if (confirmStep) {
      prim.textContent = confirmStep.label || "Confirm";
      typed.hidden = !confirmStep.text;
      prim.addEventListener("click", function () {
        var text = confirmStep.text ? typed.value.trim() : "";
        if (confirmStep.text && !text) { feedback("bad", "Type what the Board asks for first."); typed.focus(); return; }
        act({ action: "key", key: "⏎", text: text }, "Confirm");
      });
      if (frame && frame.act && frame.act.tokens.esc) {
        sec.appendChild(button("linkbtn", "Cancel", function () { confirmStep = null; act({ action: "key", key: "esc" }, "Cancel"); }));
      } else {
        sec.appendChild(button("linkbtn", "Cancel", function () { confirmStep = null; render(frame); }));
      }
      return;
    }
    typed.hidden = true;
    typed.value = "";
    var bs = cardButtons(f);
    if (!bs.length) { prim.hidden = true; return; }
    prim.hidden = false;
    bs.forEach(function (b, i) {
      var stop = !!f.paused && b.key === "x";
      var armed = stop && armedStop === (f.act && f.act.card);
      var text = armed ? "Click again to stop the run" : b.label;
      var go = function () {
        if (b.local) { $("say-text").focus(); feedback("wait", "Write what to change below and press Send."); return; }
        if (stop && !armed) {
          armedStop = f.act && f.act.card;
          feedback("wait", "Stopping keeps the run and its work; /marshal resume continues it. Click again to stop.");
          render(frame);
          setTimeout(function () { if (armedStop) { armedStop = null; render(frame); } }, 6000);
          return;
        }
        armedStop = null;
        act({ action: "key", key: b.key, confirmed: stop }, b.label);
      };
      if (i === 0) {
        prim.textContent = text;
        if (armed) prim.className = "primary danger";
        prim.addEventListener("click", go);
      } else {
        sec.appendChild(button("linkbtn" + (armed ? " danger" : ""), text, go));
      }
    });
  }

  function renderReady(f) {
    var r = f.ready, on = f.screen === "ready" || (r.checks.length > 0 && !f.top.run);
    show("p-ready", on);
    if (!on) return;
    setText("r-count", r.total ? r.ready + " of " + r.total + " ready" : "");
    var ul = clear($("r-checks"));
    var marks = { ok: ["✓ ok", "ok"], warn: ["! check", "warn"], bad: ["✗ fix", "bad"] };
    r.checks.forEach(function (c) {
      var m = marks[c.mark] || ["·", ""];
      var li = el("li");
      li.appendChild(el("span", "chip " + toneClass(m[1]), m[0]));
      li.appendChild(el("b", "", c.label));
      li.appendChild(el("span", "", c.text));
      li.appendChild(el("span", "dim", c.tag));
      if (c.hint) li.appendChild(el("span", "hint", c.hint));
      ul.appendChild(li);
    });
    var dl = clear($("r-defaults"));
    r.defaults.forEach(function (d) { dl.appendChild(el("dt", "", d.name)); dl.appendChild(el("dd", "", d.value)); });
    setText("r-checked", r.checked ? "checked " + hm(r.checked) + (r.telemetryAsk ? " · usage counts: " + (r.telemetryOn ? "on" : "off") : "") : "");
  }

  function renderPlan(f) {
    var p = f.plan, on = p.stages.length > 0 || p.total > 0;
    show("p-plan", on);
    if (!on) return;
    setText("pl-title", p.title);
    setText("pl-sum", p.total ? "· " + p.done + "/" + p.total + " merged" : "");
    setText("pl-accepted", p.accepted);
    var ol = clear($("pl-stages"));
    p.stages.forEach(function (s) {
      var st = s.state || "todo";
      var li = el("li", st, (st === "done" ? "✓ " : st === "current" ? "▶ " : "") + s.name + (s.note ? " " + s.note : ""));
      li.setAttribute("aria-label", s.name + ": " + st + (s.note ? " " + s.note : ""));
      if (st === "current") li.setAttribute("aria-current", "step");
      ol.appendChild(li);
    });
    $("pl-bar").parentNode.hidden = !p.total;
    var pct = p.total ? Math.round(100 * p.done / p.total) : 0;
    bar("pl-bar", pct, "ok");
    $("pl-bar").setAttribute("aria-valuemax", "100");
    setText("pl-counts", p.total ? p.done + "/" + p.total + " merged · " + p.active + " active · " + p.waiting + " waiting" : "");
    spark(p.throughput);
    var stats = [];
    if (p.mergedPerHr) stats.push(p.mergedPerHr + " merged/h");
    if (p.etaSec) stats.push("ETA ≈ " + dur(p.etaSec));
    if (p.reworked) stats.push(p.reworked + " reworked");
    if (p.tokens) stats.push(compact(p.tokens) + " tokens");
    setText("pl-stats", stats.join(" · "));
  }

  function spark(values) {
    var svg = $("pl-spark"), ns = "http://www.w3.org/2000/svg";
    clear(svg);
    if (!values || values.length < 2) { svg.parentNode.hidden = true; return; }
    svg.parentNode.hidden = false;
    var max = Math.max.apply(null, values.concat([1])), w = 120, h = 28, step = w / (values.length - 1);
    var pts = values.map(function (v, i) { return (i * step).toFixed(1) + "," + (h - 2 - (v / max) * (h - 4)).toFixed(1); });
    var area = document.createElementNS(ns, "polygon");
    area.setAttribute("class", "area");
    area.setAttribute("points", "0," + h + " " + pts.join(" ") + " " + w + "," + h);
    var line = document.createElementNS(ns, "polyline");
    line.setAttribute("points", pts.join(" "));
    svg.appendChild(area); svg.appendChild(line);
    svg.setAttribute("aria-label", "Merges per 5 minutes, oldest first: " + values.join(", "));
  }

  function renderLanes(f) {
    var l = f.lanes, on = l.items.length > 0 || ["running", "split", "decision", "paused"].indexOf(f.screen) >= 0 && f.top.run;
    show("p-lanes", on);
    if (!on) return;
    setText("la-limits", l.limits);
    setText("la-sum", l.items.length ? "· " + l.items.length + " running" : "");
    var tb = clear($("la-rows"));
    l.items.forEach(function (x, i) {
      var tr = el("tr", x.selected ? "sel" : "");
      tr.dataset.i = i;
      tr.appendChild(el("td", "", x.name));
      tr.appendChild(el("td", "dim", x.model || x.provider));
      tr.appendChild(el("td", "task" + (x.reviewing ? " tone-vio" : ""), x.task));
      tr.appendChild(el("td", "", x.step));
      tr.appendChild(el("td", "num elapsed"));
      tr.appendChild(el("td", "num signal"));
      tb.appendChild(tr);
    });
    show("la-empty", l.items.length === 0);
    $("la-rows").closest(".tablewrap").hidden = l.items.length === 0;
    tickLanes();
  }

  function tickLanes() {
    if (!frame) return;
    var add = since();
    Array.prototype.forEach.call($("la-rows").children, function (tr) {
      var x = frame.lanes.items[+tr.dataset.i];
      if (!x) return;
      var sig = x.lastSignalSec + add, stale = x.stale || sig > 60;
      tr.classList.toggle("stale", stale);
      tr.querySelector(".elapsed").textContent = dur(x.elapsedSec + add);
      tr.querySelector(".signal").textContent = dur(sig) + " ago" + (stale ? " · stale" : "");
    });
  }

  var taskTone = { merged: "ok", review: "vio", building: "info", waiting: "", failed: "bad", stuck: "bad", returned: "warn" };
  function renderTasks(f) {
    var on = f.tasks.length > 0 || f.screen === "running";
    show("p-tasks", on);
    var ul = clear($("ta-rows"));
    f.tasks.forEach(function (t) {
      var li = el("li");
      li.appendChild(el("span", "id", t.id));
      li.appendChild(el("span", "chip " + toneClass(taskTone[t.state]), t.state || "–"));
      li.appendChild(el("span", "title", t.title));
      var meta = [t.files, t.writer && "by " + t.writer, t.reviewer && "review " + t.reviewer].filter(Boolean).join(" · ");
      if (meta) li.appendChild(el("span", "meta", meta));
      if (t.note) li.appendChild(el("span", "chip tone-warn", t.note));
      ul.appendChild(li);
    });
    show("ta-empty", f.tasks.length === 0);
    setText("ta-sum", f.tasks.length ? "· " + f.tasks.length : "");
  }

  function renderSteps(f) {
    var s = f.steps, on = s.items.length > 0;
    show("p-steps", on);
    if (!on) return;
    setText("st-model", s.model ? "· " + s.model : "");
    var ol = clear($("st-rows"));
    s.items.forEach(function (x) {
      var li = el("li");
      li.appendChild(el("time", "", hm(x.at)));
      li.appendChild(el("span", "kind tone-info", x.kind));
      li.appendChild(el("span", "", x.text));
      li.appendChild(el("span", ""));
      ol.appendChild(li);
    });
    setText("st-quiet", s.quietSec ? "last tool call " + dur(s.quietSec) + " ago" : "");
  }

  function renderMarshal(f) {
    var m = f.marshal;
    setText("m-name", [m.provider, m.model, m.effort && m.effort + " effort"].filter(Boolean).join(" · ") || "–");
    chip($("m-use"), m.provider ? (m.inUse ? "in use" : "idle") : "", m.inUse ? "ok" : "");
    setText("m-doing", m.doing);
    bar("m-ctx", m.contextPct, m.contextPct >= 85 ? "bad" : m.contextPct >= 65 ? "warn" : "");
    setText("m-ctx-t", m.contextPct >= 0 ? m.contextPct + "%" + (m.contextWindow ? " of " + compact(m.contextWindow) : "") : "unknown");
    bar("m-5h", m.limit5hPct, leftTone(m.limit5hPct));
    setText("m-5h-t", m.limit5hPct >= 0 ? m.limit5hPct + "% left" : "unknown");
    bar("m-wk", m.weeklyPct, leftTone(m.weeklyPct));
    setText("m-wk-t", m.weeklyPct >= 0 ? m.weeklyPct + "% left" : "unknown");
    var bound = m.chatBound || f.chat.bound;
    chip($("m-chat"), bound ? "chat bound" : "chat not bound", bound ? "ok" : "warn");
    var known = m.inputKnown || f.chat.inputKnown, clearIn = m.inputClear || f.chat.inputClear;
    chip($("m-input"), known ? (clearIn ? "input clear" : "input waiting") : "", known ? (clearIn ? "ok" : "warn") : "");
    var c = f.chat, notes = [];
    if (c.restarts) notes.push(c.restarts + " restart" + (c.restarts > 1 ? "s" : "") + (c.reboundAt ? " · rebound " + hm(c.reboundAt) : ""));
    if (c.resent) notes.push(c.resent + " resent");
    if (c.answered) notes.push(c.answered + " answered");
    if (c.stage) notes.push(c.stage);
    setText("m-chatnote", notes.join(" · "));
    setText("m-sum", m.provider ? "· " + [m.provider, m.model].filter(Boolean).join(" ") : "");
  }

  function renderWorkers(f) {
    show("p-workers", f.workers.length > 0);
    setText("w-sum", f.workers.length ? "· " + f.workers.map(function (w) { return w.provider + (w.leftPct >= 0 ? " " + w.leftPct + "%" : ""); }).join(" · ") : "");
    var ul = clear($("w-rows"));
    f.workers.forEach(function (w) {
      var li = el("li");
      li.appendChild(el("b", "", (w.glyph ? w.glyph + " " : "") + w.provider));
      var b = el("div", "bar " + toneClass(leftTone(w.leftPct)));
      b.setAttribute("role", "progressbar");
      b.setAttribute("aria-label", w.provider + " limit left");
      b.setAttribute("aria-valuemin", "0"); b.setAttribute("aria-valuemax", "100");
      var fill = el("span");
      b.appendChild(fill);
      if (w.leftPct >= 0) { fill.style.width = Math.min(100, w.leftPct) + "%"; b.setAttribute("aria-valuenow", String(w.leftPct)); }
      else b.setAttribute("aria-valuetext", "unknown");
      li.appendChild(b);
      var right = el("span", "");
      right.appendChild(el("span", "chip " + toneClass(w.tagTone), w.tag || (w.leftPct >= 0 ? w.leftPct + "% left" : "")));
      right.appendChild(el("span", "dim", " " + [w.leftPct >= 0 ? w.leftPct + "% left" : "limit unknown", w.detail].filter(Boolean).join(" · ")));
      li.appendChild(right);
      if (w.reason) li.appendChild(el("span", "reason", "⚠ " + w.reason));
      ul.appendChild(li);
    });
  }

  function renderInbox(f) {
    var i = f.inbox, n = Math.max(i.count, i.lines.length);
    setText("i-count", n ? "· " + n + " waiting" : "");
    var ul = clear($("i-rows"));
    i.lines.forEach(function (l) { ul.appendChild(el("li", "", l)); });
    show("i-empty", i.lines.length === 0);
    setText("i-next", i.next ? "next ask: " + i.next : "");
  }

  function renderRequests(f) {
    show("p-requests", f.requests.length > 0);
    var tb = clear($("rq-rows"));
    f.requests.forEach(function (r) {
      var tr = el("tr");
      tr.appendChild(el("td", "num", hm(r.sent)));
      tr.appendChild(el("td", "", r.what));
      var td = el("td");
      td.appendChild(el("span", "chip " + toneClass(r.tone || (r.answer === "pending" ? "warn" : "")), r.answer || "–"));
      tr.appendChild(td);
      tr.appendChild(el("td", "num", r.latencySec ? dur(r.latencySec) : "–"));
      tb.appendChild(tr);
    });
  }

  function renderActivity(f) {
    var on = f.activity.length > 0 || !!f.top.run;
    show("p-activity", on);
    var ol = clear($("a-rows"));
    f.activity.forEach(function (e) {
      var li = el("li");
      li.appendChild(el("time", "", hm(e.at)));
      li.appendChild(el("span", "kind " + toneClass(e.tone), e.kind));
      li.appendChild(el("span", toneClass(e.tone === "muted" ? "muted" : ""), e.text));
      li.appendChild(el("span", "count", e.count > 1 ? "×" + e.count + (e.last ? " · " + hm(e.last) : "") : ""));
      ol.appendChild(li);
    });
    show("a-empty", f.activity.length === 0);
    setText("a-sum", f.activity.length ? "· " + f.activity[0].text : "");
  }

  function render(f) {
    if (!f) return;
    renderTop(f); renderNow(f); renderCard(f); renderReady(f); renderPlan(f); renderLanes(f);
    renderTasks(f); renderSteps(f); renderMarshal(f); renderWorkers(f); renderInbox(f);
    renderRequests(f); renderActivity(f);
  }

  // pending is set while an action is on its way: the buttons wait.
  var pending = false;

  function feedback(kind, text) {
    var fb = $("feedback");
    fb.hidden = !text;
    fb.className = "feedback " + kind;
    fb.textContent = text || "";
  }

  // refresh reads the frame now, so the next click has a fresh token.
  function refresh() {
    return fetch("/api/board", { headers: auth(), cache: "no-store", credentials: "omit" }).then(function (r) {
      if (!r.ok) return null;
      return r.text().then(onFrame);
    }).catch(function () { return null; });
  }

  // act sends one action for the card the page shows, with its token.
  function act(body, label) {
    if (pending) return Promise.resolve();
    if (!frame || !frame.act) { feedback("bad", "Not connected to the Board yet."); return Promise.resolve(); }
    var name = body.action === "say" ? "say" : body.key;
    var token = frame.act.tokens[name];
    if (!token) { feedback("bad", "Refused: that button is not on the Board now."); return Promise.resolve(); }
    body.card = frame.act.card;
    body.token = token;
    pending = true;
    render(frame);
    feedback("wait", (body.action === "say" ? "Sending…" : label + "…"));
    var h = auth();
    h["Content-Type"] = "application/json";
    return fetch("/api/act", { method: "POST", headers: h, body: JSON.stringify(body), cache: "no-store", credentials: "omit" })
      .then(function (r) {
        return r.json().catch(function () { return { status: "refused", text: "The Board answered " + r.status + "." }; }).then(function (res) {
          return { code: r.status, res: res || {} };
        });
      })
      .then(function (x) {
        var res = x.res, text = res.text || "";
        if (x.code === 401) { feedback("bad", "Refused: this link has expired. Run /marshal web in the Board for a new one."); return false; }
        if (x.code === 409) { confirmStep = null; feedback("bad", "Refused: " + (text || "the Board changed.")); return false; }
        if (x.code !== 200 || res.status === "refused") { feedback("bad", "Refused: " + (text || "the Board answered " + x.code + ".")); return false; }
        confirmStep = res.confirm ? { label: res.confirm.label, text: !!res.confirm.text, card: frame.act.card } : null;
        var word = res.status === "sent" ? "Sent" : res.status === "queued" ? "Queued" : res.status === "started" ? "Started" : "Done";
        feedback("ok", !text ? word + "." : text.indexOf(word) === 0 ? text : word + ": " + text);
        if (res.link) {
          $("review").hidden = false;
          $("review-link").href = res.link;
        }
        return true;
      })
      .catch(function () { feedback("bad", "Not sent: the Board is not answering."); return false; })
      .then(function (ok) {
        pending = false;
        return refresh().then(function () {
          if (confirmStep && frame && frame.act) confirmStep.card = frame.act.card;
          render(frame);
          return ok;
        });
      });
  }

  function send() {
    var box = $("say-text"), text = box.value.trim();
    if (!text) { box.focus(); return; }
    var max = frame && frame.act ? frame.act.sayMax : 2000;
    if (text.length > max) { feedback("bad", "Too long: at most " + max + " characters."); return; }
    act({ action: "say", text: text }, "Send").then(function (ok) { if (ok) box.value = ""; });
  }

  // Details start closed on a phone and open on a wide screen; after that
  // they stay as the owner left them.
  function openDetails() {
    var wide = window.matchMedia && window.matchMedia("(min-width: 861px)").matches;
    Array.prototype.forEach.call(document.querySelectorAll("details.pane"), function (d) { d.open = !!wide; });
  }

  function tick() {
    var now = new Date();
    var c = $("t-clock");
    c.textContent = pad(now.getHours()) + ":" + pad(now.getMinutes()) + ":" + pad(now.getSeconds());
    c.setAttribute("datetime", now.toISOString());
    tickLanes();
  }

  function live(state, text) {
    chip($("t-live"), text, state === "live" ? "ok" : state === "down" ? "bad" : "warn", state === "down");
    document.body.classList.toggle("is-down", state === "down");
  }

  function disconnected(why) {
    if (!downAt) downAt = performance.now();
    var o = $("offline");
    o.hidden = false;
    o.textContent = "Disconnected from the Marshal Board. " + why + (lastOK ? " What you see is from " + pad(lastOK.getHours()) + ":" + pad(lastOK.getMinutes()) + ":" + pad(lastOK.getSeconds()) + "." : "");
    live("down", "disconnected");
  }

  function onFrame(data) {
    var f;
    try { f = JSON.parse(data); } catch (e) { return; }
    if (!f || f.v !== 1) { disconnected("This page does not read this Board's format; reload it."); return; }
    frame = f; frameAt = performance.now(); lastOK = new Date(); retry = 0; downAt = 0;
    $("offline").hidden = true;
    live("live", "live");
    render(f);
  }

  // onBlock reads one Server-Sent Event: "event:" and "data:" lines; a
  // line starting with ":" is a heartbeat.
  function onBlock(block) {
    var event = "message", data = [];
    block.split("\n").forEach(function (line) {
      if (line.indexOf("event:") === 0) event = line.slice(6).trim();
      else if (line.indexOf("data:") === 0) data.push(line.slice(5).replace(/^ /, ""));
    });
    if (event === "board" && data.length) onFrame(data.join("\n"));
  }

  // connect reads /api/events with fetch, since EventSource cannot send the
  // bearer header, and reconnects with backoff when the stream ends.
  function connect() {
    retryTimer = null;
    if (!token) { disconnected("Open the link the Board printed (/marshal web)."); return; }
    live("wait", retry ? "reconnecting…" : "connecting…");
    var ctrl = typeof AbortController === "function" ? new AbortController() : null;
    var watchdog = null, ended = false;
    function arm() {
      // Heartbeats come every 15 s: a stream silent for 45 s is dead.
      clearTimeout(watchdog);
      watchdog = setTimeout(function () { if (ctrl) ctrl.abort(); }, 45000);
    }
    function again(why) {
      if (ended) return;
      ended = true;
      clearTimeout(watchdog);
      var wait = Math.min(30, Math.pow(2, retry)); retry++;
      disconnected(why + " Retrying in " + wait + "s.");
      retryTimer = setTimeout(connect, wait * 1000);
    }
    var opts = { headers: auth(), cache: "no-store", credentials: "omit" };
    if (ctrl) opts.signal = ctrl.signal;
    fetch("/api/events", opts).then(function (r) {
      if (r.status === 401) {
        // The Board closed or restarted: the link it prints now is new.
        ended = true;
        disconnected("This link has expired: run /marshal web in the Board for a new one.");
        return;
      }
      if (!r.ok || !r.body) { again("The Board answered " + r.status + "."); return; }
      var reader = r.body.getReader(), dec = new TextDecoder(), buf = "";
      arm();
      function pump() {
        return reader.read().then(function (res) {
          if (res.done) { again("The Board closed the connection."); return; }
          arm();
          buf += dec.decode(res.value, { stream: true }).replace(/\r\n?/g, "\n");
          var i;
          while ((i = buf.indexOf("\n\n")) >= 0) {
            onBlock(buf.slice(0, i));
            buf = buf.slice(i + 2);
          }
          return pump();
        });
      }
      return pump();
    }).catch(function () {
      again("The Board is not answering (closed?).");
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    tick();
    setInterval(tick, 1000);
    openDetails();
    $("say-send").addEventListener("click", send);
    $("say-text").addEventListener("keydown", function (e) {
      if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); send(); }
    });
    connect();
  });
})();
