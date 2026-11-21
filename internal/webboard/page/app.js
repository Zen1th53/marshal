// Marshal Web Board: a read-only view of the terminal Board. Every value
// from the frame is written with textContent, never as HTML.
"use strict";
(function () {
  var $ = function (id) { return document.getElementById(id); };
  var frame = null, frameAt = 0, lastOK = null;
  var es = null, retry = 0, retryTimer = null;

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
  function since() { return frame ? (performance.now() - frameAt) / 1000 : 0; }
  function keyItem(k) {
    var li = el("li");
    li.appendChild(el("kbd", "", k.key));
    li.appendChild(document.createTextNode(k.label));
    return li;
  }
  function keyList(id, keys) {
    var ul = clear($(id));
    (keys || []).forEach(function (k) { ul.appendChild(keyItem(k)); });
  }
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
    setText("t-run", t.run || "no run");
    chip($("t-state"), t.state || "–", t.stateTone, true);
    setText("t-workers", t.workers);
    setText("t-inbox", t.inbox);
    document.title = (t.state ? t.state + " · " : "") + (t.project || "Marshal") + " · Marshal Board";
  }

  function renderNow(f) {
    var n = f.nowLine;
    setText("n-marshal", n.marshal || "–");
    setText("n-work", n.work || "–");
    setText("n-you", n.fromYou || "nothing");
    $("n-you-box").className = "you" + (n.fromYouOK ? "" : " act");
    chip($("n-you-tag"), n.fromYouOK ? "nothing needed" : "your turn", n.fromYouOK ? "ok" : "warn", !n.fromYouOK);
  }

  function renderCard(f) {
    var card = $("card"), d = f.decision, p = f.paused;
    if (!d && !p) { card.hidden = true; return; }
    card.hidden = false;
    card.className = "card" + (p && !d ? " paused" : "");
    clear($("card-nodes")); clear($("card-lines")); clear($("card-tasks"));
    if (d) {
      setText("card-title", d.title);
      setText("card-corner", d.corner || "");
      $("card-self").hidden = !d.selfApproval;
      setText("card-self", d.selfApproval);
      setText("card-summary", d.summary);
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
      setText("card-will", "");
      setText("card-extra", d.hasLink ? (d.noDisplay ? "The review link is shown in the terminal Board (c copies it)." : "The review opens in your browser from the terminal Board.") : "");
      var k = d.actions[0];
      setText("card-how", k ? "Answer in the Marshal Board (key " + k.key + ": " + k.label + ")." : "Answer in the Marshal Board.");
      keyList("card-keys", d.actions);
    } else {
      setText("card-title", p.title);
      setText("card-corner", p.resets);
      $("card-self").hidden = true;
      setText("card-summary", p.cause);
      setText("card-changed", "");
      p.tasks.forEach(function (t) {
        var li = el("li");
        li.appendChild(el("b", "", t.title));
        li.appendChild(el("span", "dim", " · " + [t.progress, t.kept].filter(Boolean).join(" · ")));
        $("card-tasks").appendChild(li);
      });
      setText("card-will", p.will ? "⏎ will " + p.will.split("\n")[0] : "");
      setText("card-extra", [p.will && p.will.split("\n")[1], p.room, p.run].filter(Boolean).join(" · "));
      var pk = p.actions[0];
      setText("card-how", pk ? "Choose in the Marshal Board (key " + pk.key + ": " + pk.label + ")." : "Choose in the Marshal Board.");
      keyList("card-keys", p.actions);
    }
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
    keyList("r-keys", r.actions);
  }

  function renderPlan(f) {
    var p = f.plan, on = p.stages.length > 0 || p.total > 0;
    show("p-plan", on);
    if (!on) return;
    setText("pl-title", p.title ? "· " + p.title : "");
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
    setText("la-limits", l.limits ? "· " + l.limits : "");
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
  }

  function renderWorkers(f) {
    show("p-workers", f.workers.length > 0);
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
  }

  function render(f) {
    renderTop(f); renderNow(f); renderCard(f); renderReady(f); renderPlan(f); renderLanes(f);
    renderTasks(f); renderSteps(f); renderMarshal(f); renderWorkers(f); renderInbox(f);
    renderRequests(f); renderActivity(f);
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
    var o = $("offline");
    o.hidden = false;
    o.textContent = "Disconnected from the Marshal Board. " + why + (lastOK ? " What you see is from " + pad(lastOK.getHours()) + ":" + pad(lastOK.getMinutes()) + ":" + pad(lastOK.getSeconds()) + "." : "");
    live("down", "disconnected");
  }

  function connect() {
    retryTimer = null;
    live("wait", retry ? "reconnecting…" : "connecting…");
    es = new EventSource("/api/events");
    es.addEventListener("board", function (ev) {
      var f;
      try { f = JSON.parse(ev.data); } catch (e) { return; }
      if (!f || f.v !== 1) { disconnected("This page does not read this Board's format; reload it."); return; }
      frame = f; frameAt = performance.now(); lastOK = new Date(); retry = 0;
      $("offline").hidden = true;
      live("live", "live");
      render(f);
    });
    es.onerror = function () {
      es.close(); es = null;
      var wait = Math.min(30, Math.pow(2, retry)); retry++;
      // A refused session means the Board closed or restarted: the link
      // it prints now is a new one.
      fetch("/api/board", { credentials: "same-origin", cache: "no-store" }).then(function (r) {
        if (r.status === 403) { disconnected("This link has expired: run /marshal web in the Board for a new one."); return; }
        disconnected("Retrying in " + wait + "s.");
        retryTimer = setTimeout(connect, wait * 1000);
      }, function () {
        disconnected("The Board is not answering (closed?). Retrying in " + wait + "s.");
        retryTimer = setTimeout(connect, wait * 1000);
      });
    };
  }

  document.addEventListener("DOMContentLoaded", function () {
    tick();
    setInterval(tick, 1000);
    connect();
  });
})();
