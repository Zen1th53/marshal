// Marshal Web Board: the owner's interface to the Board, in sections (Home,
// Work, Architecture, Team, Activity, Settings) behind hash routes, so Back
// and deep links (#work/<task>) work. When the Board waits for the owner, the
// card with its buttons is at the top of Home and the "From you" pill links
// to it; a click presses that button on the Board (POST /api/act with the
// frame's one-use token). Every value from the frame is written with
// textContent, never as HTML.
"use strict";
(function () {
  var $ = function (id) { return document.getElementById(id); };
  var frame = null, frameAt = 0, lastOK = null;
  var retry = 0, retryTimer = null;

  // The link's secret (?t=…) works once: it is taken out of the address bar
  // and the history at once (the route after # stays) and spent (POST /api/session) for the page's
  // own API secret, which is kept for this tab only, so a reload still
  // works. A copy of the link left anywhere else is then worthless. The
  // secret goes to the server as a bearer header; no cookie is used.
  var tokenKey = "marshal-board-session";
  var linkSecret = null, token = null;
  try { linkSecret = new URLSearchParams(location.search).get("t"); } catch (e) { linkSecret = null; }
  if (linkSecret) {
    try { history.replaceState(null, "", "/" + location.hash); } catch (e) { /* keep going */ }
  } else {
    try { token = sessionStorage.getItem(tokenKey); } catch (e) { token = null; }
  }
  function auth() { return { "Authorization": "Bearer " + token }; }

  // spendLink trades the link for the API secret, then calls next. A link
  // already used is refused, and the page says so.
  function spendLink(next) {
    if (!linkSecret) { next(); return; }
    var opened = linkSecret;
    linkSecret = null;
    fetch("/api/session", { method: "POST", headers: { "Authorization": "Bearer " + opened }, cache: "no-store", credentials: "omit" })
      .then(function (r) {
        return r.json().then(function (d) { return { ok: r.ok, d: d || {} }; });
      })
      .then(function (x) {
        if (x.ok && x.d.session) {
          token = x.d.session;
          try { sessionStorage.setItem(tokenKey, token); } catch (e) { /* in memory only */ }
          next();
          return;
        }
        disconnected(x.d.text || "This link does not work any more. Run /marshal web for a new one.");
      })
      .catch(function () { disconnected("The Board is not answering (closed?)."); });
  }

  var docsURL = "https://marshal.blackhat.uz/docs";

  // asksOwner: the Marshal asked the owner something in its chat and waits
  // (the Board's From-you line says so: nowLine.chatAsked).
  function asksOwner(f) {
    return !!(f.nowLine && f.nowLine.chatAsked);
  }

  // ---- small helpers ----------------------------------------------------
  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined && text !== null) e.textContent = String(text);
    return e;
  }
  function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); return node; }
  function show(id, on) { $(id).hidden = !on; }
  function setText(id, text) { $(id).textContent = text === undefined || text === null ? "" : String(text); }
  function toneClass(t) { return t ? "tone-" + t : ""; }
  function chip(node, text, tone) {
    node.textContent = text || "";
    node.className = "chip" + (tone ? " " + toneClass(tone) : "");
  }
  function pad(n) { return n < 10 ? "0" + n : "" + n; }
  function parse(iso) { if (!iso) return null; var d = new Date(iso); return isNaN(d) ? null : d; }
  function hm(iso) { var d = parse(iso); return d ? pad(d.getHours()) + ":" + pad(d.getMinutes()) : ""; }
  var months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  var days = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
  // when is a time of day, with the date when it is not today.
  function when(iso) {
    var d = parse(iso);
    if (!d) return "";
    var n = new Date();
    var today = d.getFullYear() === n.getFullYear() && d.getMonth() === n.getMonth() && d.getDate() === n.getDate();
    return (today ? "" : days[d.getDay()] + " " + d.getDate() + " " + months[d.getMonth()] + " ") + pad(d.getHours()) + ":" + pad(d.getMinutes());
  }
  function dur(s) {
    if (s === undefined || s === null || s < 0) return "–";
    s = Math.floor(s);
    if (s < 60) return s + "s";
    var m = Math.floor(s / 60);
    if (m < 60) return m + "m";
    if (m < 48 * 60) return Math.floor(m / 60) + "h " + pad(m % 60) + "m";
    return Math.floor(m / 1440) + "d";
  }
  function ago(iso) {
    var d = parse(iso);
    if (!d) return "";
    var s = (Date.now() - d.getTime()) / 1000;
    return s < 30 ? "just now" : dur(s) + " ago";
  }
  function compact(n) {
    if (!n) return "0";
    if (n >= 1e6) return (n / 1e6).toFixed(1).replace(/\.0$/, "") + "M";
    if (n >= 1e3) return Math.round(n / 1e3) + "k";
    return String(n);
  }
  function cap(s) { s = String(s || ""); return s.charAt(0).toUpperCase() + s.slice(1); }
  function sentence(text) {
    text = String(text || "").trim();
    if (!text) return "";
    text = cap(text);
    return /[.!?…]$/.test(text) ? text : text + ".";
  }
  function plural(n, one, many) { return n + " " + (n === 1 ? one : many || one + "s"); }

  // since is how long ago the frame came; it stops while disconnected, so
  // the clocks do not run on data that is no longer live.
  var downAt = 0;
  function since() { return frame ? ((downAt || performance.now()) - frameAt) / 1000 : 0; }
  function setBar(b, pct, tone) {
    var known = pct !== undefined && pct !== null && pct >= 0;
    var v = known ? Math.max(0, Math.min(100, pct)) : 0;
    b.firstElementChild.style.width = v + "%";
    b.className = "bar" + (tone ? " " + toneClass(tone) : "");
    if (known) { b.setAttribute("aria-valuenow", String(v)); b.removeAttribute("aria-valuetext"); }
    else { b.removeAttribute("aria-valuenow"); b.setAttribute("aria-valuetext", "unknown"); }
  }
  function newBar(label, pct, tone) {
    var b = el("div", "bar");
    b.setAttribute("role", "progressbar");
    b.setAttribute("aria-label", label);
    b.setAttribute("aria-valuemin", "0"); b.setAttribute("aria-valuemax", "100");
    b.appendChild(el("span"));
    setBar(b, pct, tone);
    return b;
  }
  function leftTone(p) { return p < 0 ? "" : p <= 15 ? "bad" : p <= 35 ? "warn" : "ok"; }
  function link(href, text, cls) { var a = el("a", cls, text); a.href = href; return a; }
  function kv(dl, name, value, cls) {
    if (value === undefined || value === null || value === "") return;
    dl.appendChild(el("dt", "", name));
    var dd = el("dd", cls || "");
    if (value instanceof Node) dd.appendChild(value); else dd.textContent = String(value);
    dl.appendChild(dd);
  }

  // Sections are rebuilt only when what they show changed, so a frame that
  // only moved a clock does not take the focus from under the owner.
  var sigs = {};
  function changed(name, value) {
    var s = JSON.stringify(value);
    if (sigs[name] === s) return false;
    sigs[name] = s;
    return true;
  }

  // ---- routes -------------------------------------------------------------
  var sections = ["home", "work", "architecture", "team", "activity", "settings"];
  var route = { name: "home", task: "" };
  function readRoute() {
    var h = decodeURIComponent((location.hash || "").replace(/^#/, ""));
    var parts = h.split("/");
    var name = sections.indexOf(parts[0]) >= 0 ? parts[0] : "home";
    return { name: name, task: name === "work" && parts[1] ? parts[1] : "" };
  }
  function applyRoute(focus) {
    route = readRoute();
    sections.forEach(function (s) { $("s-" + s).hidden = s !== route.name; });
    Array.prototype.forEach.call(document.querySelectorAll("a.nav"), function (a) {
      if (a.dataset.route === route.name) a.setAttribute("aria-current", "page"); else a.removeAttribute("aria-current");
    });
    var titles = { home: "Home", work: "Work", architecture: "Architecture", team: "Team", activity: "Activity", settings: "Settings" };
    titleRoute = titles[route.name];
    if (frame) render(frame);
    if (focus) {
      if (route.task && !$("drawer").hidden) {
        // The task's details, wherever the layout put them.
        $("dr-title").setAttribute("tabindex", "-1");
        $("dr-title").focus({ preventScroll: true });
        $("drawer").scrollIntoView({ block: "start" });
      } else {
        $("main").focus({ preventScroll: true });
        window.scrollTo(0, 0);
      }
    }
  }
  var titleRoute = "Home";

  // ---- the frame's words ---------------------------------------------------
  // Raw ids (decision-<hex>, long hex ids) mean nothing to the owner: a
  // "Marshal decision requested" line becomes what the Marshal asked for.
  var hexID = /\b(?:decision-)?[0-9a-f]{12,}\b/gi;
  function plainText(text) {
    text = String(text || "");
    var m = /^Marshal decision requested:\s*decision-[0-9a-z]+\s*(\S*)\s*(.*)$/i.exec(text.split("\n")[0]);
    if (m) {
      var kind = (m[1] || "a decision").replace(/[-_]/g, " ");
      var obj = (m[2] || "").replace(/\b[A-Z]+(?:-[A-Z]+)*-[0-9a-f]{8,}\b/g, "").replace(hexID, "").trim();
      return "The Marshal asked for " + kind + (obj ? " (" + obj + ")" : "");
    }
    return text.replace(/\bdecision-[0-9a-f]{6,}\b/gi, "a decision").replace(hexID, "…");
  }

  var taskTone = { merged: "ok", review: "vio", building: "info", waiting: "", cancelled: "muted" };
  function rework(t) { var m = /^rework (\d+)/.exec(t.note || ""); return m ? +m[1] : 0; }
  function laneFor(f, id) {
    for (var i = 0; i < f.lanes.items.length; i++) {
      var x = f.lanes.items[i];
      if (!x.reviewing && (x.task === id || x.task.indexOf(id + " ") === 0)) return { lane: x, i: i };
    }
    return null;
  }
  function reviewLaneFor(f, id) {
    for (var i = 0; i < f.lanes.items.length; i++) {
      var x = f.lanes.items[i];
      if (x.reviewing && new RegExp("\\b" + id + "\\b").test(x.task)) return { lane: x, i: i };
    }
    return null;
  }
  function bucket(t) {
    if (t.state === "merged") return "merged";
    if (rework(t)) return "sentback";
    if (t.state === "building") return "building";
    if (t.state === "waiting") return "waiting";
    return "other";
  }
  // stateWords is a task's state in plain words; the running time is a
  // span the clock fills.
  function stateWords(f, t) {
    var out = el("span", "st");
    var n = rework(t);
    var word;
    switch (t.state) {
      case "building": word = n ? "sent back " + n + "×, building" : "building"; break;
      case "review": word = t.note === "merging" ? "accepted, merging" : "in review"; break;
      case "waiting": word = t.waiting ? t.waiting.label : t.note ? t.note : "waiting"; break;
      default: word = t.state || "–";
    }
    var c = el("span", "chip " + toneClass(n ? "warn" : /stuck/.test(t.note || "") ? "bad" : taskTone[t.state]), word);
    out.appendChild(c);
    var l = t.state === "building" ? laneFor(f, t.id) : null;
    if (l) { out.appendChild(document.createTextNode(" ")); var s = el("span", "muted el"); s.dataset.lane = l.i; out.appendChild(s); }
    return out;
  }
  // finished: the Board says DONE, or every task is merged or cancelled
  // and none is active.
  function cancelledCount(f) { return f.tasks.filter(function (t) { return t.state === "cancelled"; }).length; }
  // merged counts the task rows when the frame has them, else the plan's.
  function merged(f) { return f.tasks.length ? f.tasks.filter(function (t) { return t.state === "merged"; }).length : f.plan.done; }
  function finished(f) {
    if (f.top.state === "DONE") return true;
    return f.plan.total > 0 && f.plan.done + cancelledCount(f) >= f.plan.total && f.plan.active === 0;
  }
  // phase is what kind of moment this is, for the empty states.
  function phase(f) {
    if (f.paused) return "paused";
    if (f.lastRun) return "finished";
    if (f.screen === "ready" || (!f.top.run && f.screen !== "planning" && f.screen !== "decision" && !f.plan.stages.length)) return "norun";
    if (f.screen === "planning" || (f.screen === "decision" && !f.top.run)) return "planning";
    if (finished(f)) return "finished";
    return "run";
  }
  function emptyBox(id, title, text) {
    var box = clear($(id));
    box.appendChild(el("b", "", title));
    box.appendChild(el("span", "", text));
    box.hidden = false;
  }

  // ---- owner actions -----------------------------------------------------
  // Keys that only change the terminal's own screen are not offered here:
  // c copies to the terminal's clipboard, 7 opens a terminal view, e edits
  // in the terminal, q quits the terminal Board, esc steps the card aside.
  var terminalOnly = { "c": 1, "7": 1, "e": 1, "q": 1, "esc": 1 };
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
  function screenKey(f, key) {
    if (f.decision || f.paused || !f.act || !f.act.tokens[key]) return null;
    for (var i = 0; i < f.keys.length; i++) if (f.keys[i].key === key) return f.keys[i];
    return null;
  }
  // retryTask is the task the screen's Retry would start again, or "".
  function retryTask(f) {
    var k = screenKey(f, "r");
    var m = k && /^retry\s+(\S+)/i.exec(k.label);
    return m ? m[1] : "";
  }

  var armedStop = null, confirmStep = null, armed = null, pending = false;
  // arm is the first click of a two-step action: it says what the second
  // click does and disarms itself after 6 seconds.
  function arm(id, text) {
    armed = id;
    feedback("wait", text);
    render(frame);
    setTimeout(function () { if (armed === id) { armed = null; render(frame); } }, 6000);
  }
  function twoStep(cls, key, label, warn) {
    if (key === "r" && frame) {
      var retry = workCapabilities(frame, retryTask(frame)).find(function (c) { return c.op === "task/retry"; });
      if (retry) { var typed = el("button", cls, label); typed.type = "button"; typed.onclick = function () { previewSetting(newWorkRequest(retry, {})); }; return typed; }
    }
    if (key === "ctrl+x" && frame) {
      // The screen's stop key takes the same bound request as the run control.
      var workers = runCapabilities(frame).find(function (c) { return c.op === "run/stop-workers"; });
      if (workers) { var bound = el("button", cls, label); bound.type = "button"; bound.onclick = function () { previewSetting(newWorkRequest(workers, {})); }; return bound; }
    }
    var id = (frame.act ? frame.act.card : "") + "|" + key;
    var on = armed === id;
    return button(cls + (on ? " danger" : ""), on ? "Click again to " + label : cap(label), function () {
      if (armed !== id) { arm(id, warn); return; }
      armed = null;
      act({ action: "key", key: key, confirmed: true }, cap(label));
    });
  }
  var retryWarn = "Retrying starts the task again from scratch in a fresh session, keeping its branch. Click again to retry.";
  var stopAllWarn = "This stops every worker; the Marshal keeps running. Click again to stop them.";

  function button(cls, text, onClick) {
    var b = el("button", cls, text);
    b.type = "button";
    b.disabled = pending;
    b.addEventListener("click", onClick);
    return b;
  }

  function renderCard(f) {
    var card = $("card"), d = f.decision, p = f.paused;
    var cardID = f.act ? f.act.card : "";
    if (confirmStep && confirmStep.card !== cardID && !pending) confirmStep = null;
    if (!changed("card", [cardID, !!f.act, d, p, pending, confirmStep, armedStop])) return;
    if ((!d && !p) || (asksOwner(f) && d && d.kind === "architecture")) { card.hidden = true; confirmStep = null; return; }
    card.hidden = false;
    card.className = "card" + (p && !d ? " paused" : "");
    clear($("card-nodes")); clear($("card-lines")); clear($("card-tasks"));
    var more = false;
    var kinds = { architecture: "the architecture", inbox: "a request waiting in the inbox", plan: "the plan" };
    setText("card-who", d ? "What is decided: " + (kinds[d.kind] || (d.kind ? d.kind.replace(/[-_]/g, " ") : "this card")) + " · decided by: you, never the Marshal" :
      "What is decided: how the run goes on · decided by: you");
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
        if (l.note) dd.appendChild(el("span", "muted", " · " + l.note));
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
        li.appendChild(el("span", "muted", " · " + [t.progress, t.kept].filter(Boolean).join(" · ")));
        $("card-tasks").appendChild(li);
      });
      setText("card-extra", [p.will && p.will.split("\n").slice(1).join(" "), p.room, p.run].filter(Boolean).join(" · "));
      more = !!(p.tasks.length || p.room || p.run);
    }
    $("d-card").hidden = !more;
    renderButtons(f);
  }

  // renderButtons draws the primary and the secondary buttons, or the
  // confirmation the Board waits for after a first press.
  function renderButtons(f) {
    var prim = $("primary"), sec = clear($("secondary")), typed = $("confirm-text");
    var fresh = prim.cloneNode(false);
    prim.parentNode.replaceChild(fresh, prim);
    prim = fresh;
    prim.className = "primary";
    prim.disabled = pending || !f.act;
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
        sec.appendChild(button("linkbtn", "Cancel", function () { confirmStep = null; sigs.card = ""; render(frame); }));
      }
      return;
    }
    typed.hidden = true;
    typed.value = "";
    var bs = cardButtons(f);
    var d = f.decision;
    if (d && !d.summary && !d.lines.length && !d.nodes.length && !d.corner) {
      // The frame does not carry what this decision rests on: it is not
      // offered here.
      prim.hidden = true;
      sec.appendChild(el("span", "tone-warn", "Decide this in the terminal Board: this page does not have the facts it rests on."));
      return;
    }
    if (!bs.length) { prim.hidden = true; return; }
    prim.hidden = false;
    bs.forEach(function (b, i) {
      var stop = !!f.paused && b.key === "x";
      var on = stop && armedStop === (f.act && f.act.card);
      var text = on ? "Click again to stop the run" : b.label;
      var go = function () {
        if (b.local) { location.hash = "#home"; $("say-text").focus(); feedback("wait", "Write what to change in \"Tell the Marshal\" and press Send."); return; }
        if (stop && !on) {
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
        if (on) prim.className = "primary danger";
        prim.addEventListener("click", go);
      } else {
        sec.appendChild(button("linkbtn" + (on ? " danger" : ""), text, go));
      }
    });
  }

  // ---- header and nav ------------------------------------------------------
  function renderTop(f) {
    var t = f.top;
    setText("t-version", t.version);
    setText("t-project", t.project || "–");
    setText("t-run", t.run ? "· " + t.run : "");
    chip($("t-state"), t.state || "–", t.stateTone);
    document.title = titleRoute + " · " + (t.state ? t.state + " · " : "") + (t.project || "Marshal") + " · Marshal Board";
    var n = f.nowLine, pill = $("you-pill");
    // A question the Marshal asked in chat is what the owner is asked for
    // takes priority over an architecture card; the Board's From-you line says it.
    var asked = asksOwner(f);
    var needs = (!n.fromYouOK && !asked) || !!f.decision || !!f.paused;
    var you = n.fromYou || "nothing";
    setText("you", you);
    pill.className = "pill" + (needs || asked ? " act" : "");
    var rt = retryTask(f);
    pill.setAttribute("href", f.decision || f.paused ? "#home" : asked ? "#home" : rt ? "#work/" + rt : "#home");
    pill.setAttribute("aria-label", "From you: " + you + (needs || asked ? " (needs you)" : ""));
    // A dot (with words for a screen reader) where the owner must act.
    var dots = { home: !!(f.decision || f.paused) || asked, work: !!rt, team: f.ready.checks.some(function (c) { return c.mark !== "ok" && c.tag === "action"; }) };
    ["home", "work", "architecture", "team", "activity"].forEach(function (s) {
      $("n-" + s).querySelector(".dot").hidden = !dots[s];
    });
    setText("n-work-count", f.plan.total ? f.plan.done + "/" + f.plan.total : "");
    if (f.plan.total) $("n-work").setAttribute("aria-label", "Work, " + f.plan.done + " of " + f.plan.total + " merged" + (dots.work ? " (needs you)" : ""));
    else $("n-work").removeAttribute("aria-label");
  }

  // ---- Home ------------------------------------------------------------------
  function heroSentence(f) {
    var n = f.nowLine;
    if (f.decision) return "The Marshal is waiting on you: " + sentence(f.decision.title);
    if (f.paused) return "The run waits for your choice." + (n.work ? " Work: " + sentence(n.work) : "");
    if (f.lastRun) return n.work + ". Tell the Marshal a new goal below.";
    var ph = phase(f);
    if (ph === "finished") return "Finished: " + plural(merged(f), "task") + " merged" + (cancelledCount(f) ? ", " + cancelledCount(f) + " cancelled." : ".");
    var parts = [];
    if (n.marshal) parts.push("The Marshal: " + n.marshal);
    if (n.work) parts.push("Work: " + n.work);
    return parts.length ? parts.map(sentence).join(" ") : "Connected to the Board.";
  }
  function renderHome(f) {
    setText("now", heroSentence(f));
    var m = f.mode || {};
    var flag = function (v) { return v == null ? "unknown" : v ? "on" : "off"; };
    var number = function (v) { return v == null ? "unknown" : String(v); };
    setText("execution-mode", (m.edition || "unknown") + " · entitlement " + (m.entitlement || "unknown") +
      " · preference " + flag(m.executionEnabled) + " · effective " + (m.effectiveTier || "unknown") +
      " · active " + number(m.active) + " / cap " + number(m.effectiveCap) +
      " · plan " + (m.planParallel == null ? "unknown" : m.planParallel ? "parallel" : "serial") +
      " · recorded " + (m.recordedRunTier || "unknown") + " · delegation " + flag(m.delegation) +
      " · request " + (m.requestStatus || "unknown"));
    setText("execution-reasons", (m.reasons || []).join("; "));
    var p = f.plan, nums = clear($("nums"));
    var bits = [];
    if (p.total) bits.push([p.done + " of " + p.total, " merged"]);
    if (p.active) bits.push([String(p.active), " active"]);
    if (p.waiting) bits.push([String(p.waiting), " waiting"]);
    if (f.top.workers) bits.push([String(f.top.workers), " workers"]);
    if (p.mergedPerHr) bits.push([String(p.mergedPerHr), " merged per hour"]);
    if (p.reworked) bits.push([String(p.reworked), " sent back"]);
    if (p.etaSec > 0) bits.push(["≈ " + dur(p.etaSec), " to go"]);
    bits.forEach(function (b, i) {
      if (i) nums.appendChild(document.createTextNode(" · "));
      nums.appendChild(el("b", "", b[0]));
      nums.appendChild(document.createTextNode(b[1]));
    });
    show("home-bar-row", p.total > 0);
    if (p.total) setBar($("home-bar"), Math.round(100 * p.done / p.total), "ok");

    var ph = phase(f);
    $("home-empty").hidden = true;
    if (ph === "norun" && !f.decision) firstRun(f);
    else if (ph === "planning" && !f.decision) emptyBox("home-empty", "The Marshal is planning", "Tasks appear in Work once the plan is ready. Its steps are in Activity.");
    else if (ph === "finished") {
      var cancelled = f.tasks.filter(function (t) { return t.state === "cancelled"; }).length;
      emptyBox("home-empty", "Finished" + (f.top.run ? ": " + f.top.run : ""),
        plural(f.tasks.length ? f.tasks.filter(function (t) { return t.state === "merged"; }).length : p.done, "task") + " merged" + (cancelled ?", " + cancelled + " cancelled" : ", none failed") +
        ". What next? Tell the Marshal below; the run's own branch and its final review are in the terminal Board.");
    }
    renderSummary(f, ph);
    renderSays(f);
    renderLatest(f);
  }
  // renderSays is the Marshal's last chat turn to the owner, above the box
  // that answers it.
  function renderSays(f) {
    var m = f.marshalSays || {};
    if (!changed("says", [m, asksOwner(f)])) return;
    var has = !!m.text;
    setText("says-text", has ? m.text : "");
    show("says-empty", !has);
    var meta = [];
    if (m.at) meta.push("said " + when(m.at));
    if (asksOwner(f)) meta.push("asks you; answer in “Tell the Marshal” below");
    else if (m.answered) meta.push("answered");
    setText("says-meta", meta.join(" · "));
    $("says-meta").className = asksOwner(f) ? "tone-warn" : "muted";
    show("says-more", !!m.truncated);
  }
  // firstRun says plainly what must still be finished in the terminal and
  // why: those prompts belong to the CLIs themselves, which this page
  // cannot reach.
  function firstRun(f) {
    var box = clear($("home-empty"));
    box.hidden = false;
    box.appendChild(el("b", "", "No run yet"));
    box.appendChild(el("p", "", "Tell the Marshal what to build below. A few first steps happen only in the terminal, because they are the provider CLIs' own prompts and this page cannot answer them for you:"));
    var ul = el("ul", "plain");
    var todo = f.ready.checks.filter(function (c) { return c.mark !== "ok"; });
    todo.forEach(function (c) {
      var li = el("li");
      li.appendChild(el("b", "", (c.mark === "bad" ? "✗ " : "! ") + c.label + ": "));
      li.appendChild(document.createTextNode(c.text + (c.hint ? " (" + c.hint.replace(/\s+/g, " ").trim() + ")" : "")));
      ul.appendChild(li);
    });
    ["Choosing the Marshal: the picker when the Board first opens.",
      "A provider CLI's own questions, such as trusting this folder.",
      "Signing in to a provider (agy, claude, codex) in its own window."].forEach(function (t) { ul.appendChild(el("li", "muted", t)); });
    box.appendChild(ul);
    box.appendChild(link("#team", "See the team's readiness"));
  }
  function renderSummary(f, ph) {
    var ul = $("sum-cards");
    var on = (ph === "run" || ph === "paused") && f.tasks.length > 0;
    var building = f.tasks.filter(function (t) { return t.state === "building"; });
    var back = f.tasks.filter(function (t) { return rework(t) > 0; });
    var warn = f.workers.filter(function (w) { return w.reason || w.tagTone === "bad" || w.tagTone === "warn"; });
    if (!changed("summary", [on, building.map(function (t) { return t.id; }), back.map(function (t) { return t.id; }),
      warn.map(function (w) { return [w.provider, w.reason, w.tag]; }), f.lanes.items.length])) return;
    clear(ul);
    ul.hidden = !on;
    if (!on) return;
    function card(href, label, big, sub, cls) {
      var li = el("li"), a = link(href, "", cls);
      a.appendChild(el("span", "lbl", label));
      a.appendChild(el("span", "big", big));
      a.appendChild(el("span", "sub", sub));
      li.appendChild(a);
      ul.appendChild(li);
    }
    var ids = function (ts) { return ts.slice(0, 3).map(function (t) { return t.id; }).join(", ") + (ts.length > 3 ? " and " + (ts.length - 3) + " more" : ""); };
    card("#work", "Building now", String(building.length), building.length ? ids(building) + " · " + plural(f.lanes.items.length, "worker") + " busy" : "nothing is being built");
    card(back.length ? "#work/" + back[0].id : "#work", "Sent back", String(back.length), back.length ? ids(back) + ": reviewers asked for changes" : "no task was sent back", back.length ? "warn" : "");
    card("#team", "Team warning", warn.length ? String(warn.length) : "none",
      warn.length ? warn[0].provider + ": " + (warn[0].reason || warn[0].tag) : "every provider is fine", warn.length ? "warn" : "");
  }
  function meaningful(e) { return e.tone !== "muted"; }
  function eventRow(e) {
    var li = el("li");
    li.appendChild(el("time", "", hm(e.at)));
    li.appendChild(el("span", "kind " + toneClass(e.tone), e.kind));
    li.appendChild(el("span", "text", plainText(e.text)));
    li.appendChild(el("span", "count", e.count > 1 ? "×" + e.count + (e.last ? " · last " + hm(e.last) : "") : ""));
    return li;
  }
  function renderLatest(f) {
    var list = f.activity.filter(meaningful).slice(0, 3);
    if (!changed("latest", list)) return;
    var ol = clear($("latest"));
    list.forEach(function (e) { ol.appendChild(eventRow(e)); });
    show("latest-empty", list.length === 0);
  }

  // ---- Work ------------------------------------------------------------------
  var filter = "all";
  var filters = [["all", "All"], ["building", "Building"], ["sentback", "Sent back"], ["waiting", "Waiting"], ["merged", "Merged"]];
  function renderWork(f) {
    var p = f.plan, ph = phase(f);
    setText("work-sum", f.lastRun ? f.nowLine.work + " · read-only" : p.total ? p.done + " of " + p.total + " merged" + (p.title ? " · " + p.title : "") : p.title);
    if (changed("stages", p.stages)) {
      var ol = clear($("stages"));
      p.stages.forEach(function (s) {
        var st = s.state || "todo";
        var li = el("li", st, (st === "done" ? "✓ " : st === "current" ? "▶ " : "") + s.name + (s.note ? " " + s.note : ""));
        li.setAttribute("aria-label", s.name + ": " + (st === "current" ? "now" : st) + (s.note ? ", " + s.note : ""));
        if (st === "current") li.setAttribute("aria-current", "step");
        ol.appendChild(li);
      });
    }
    renderControls(f);
    var none = f.tasks.length === 0;
    $("work-empty").hidden = true;
    if (none) {
      if (ph === "planning") emptyBox("work-empty", "Planning", "The Marshal is writing the plan; its tasks appear here when it is ready.");
      else if (ph === "norun") emptyBox("work-empty", "No run yet", "Tell the Marshal what to build on Home; the plan and its tasks show up here.");
      else if (ph === "paused") emptyBox("work-empty", "Paused", "The run waits for your choice on Home. Its task list is not sent with this card.");
      else emptyBox("work-empty", "No tasks yet", "The run has no tasks on the Board yet.");
    }
    show("work-body", !none);
    renderFilters(f);
    renderTasks(f);
    renderDrawer(f);
    renderPlan(f);
    renderLanes(f);
  }
  function renderControls(f) {
    var rt = retryTask(f), stop = screenKey(f, "ctrl+x");
    var rtTask = f.tasks.filter(function (x) { return x.id === rt; });
    if (!changed("controls", [rt, rtTask, !!stop, f.lanes.items.length, armed, pending, f.act && f.act.card])) return;
    var box = clear($("controls"));
    // Each button with the facts it decides on next to it.
    if (rt) {
      var t = null;
      f.tasks.forEach(function (x) { if (x.id === rt) t = x; });
      box.appendChild(twoStep("ghost", "r", "retry " + rt + " from scratch", retryWarn));
      box.appendChild(el("span", "muted", rt + (t ? " · " + t.title + (t.note ? " · " + t.note : "") : "") + " · starts again in a fresh session, keeps its branch"));
    }
    if (stop) {
      box.appendChild(twoStep("ghost", "ctrl+x", "stop all workers", stopAllWarn));
      box.appendChild(el("span", "muted", plural(f.lanes.items.length, "worker") + " running · the Marshal keeps running"));
    }
    box.hidden = !box.firstChild;
  }
  function renderFilters(f) {
    var counts = { all: f.tasks.length, building: 0, sentback: 0, waiting: 0, merged: 0 };
    f.tasks.forEach(function (t) { var b = bucket(t); if (counts[b] !== undefined) counts[b]++; });
    if (!changed("filters", [counts, filter])) return;
    var box = clear($("filters"));
    filters.forEach(function (x) {
      var b = el("button", "", x[1]);
      b.type = "button";
      b.setAttribute("aria-pressed", String(filter === x[0]));
      b.appendChild(el("span", "n", String(counts[x[0]])));
      b.addEventListener("click", function () { filter = x[0]; render(frame); });
      box.appendChild(b);
    });
  }
  function renderTasks(f) {
    var rows = f.tasks.filter(function (t) { return filter === "all" || bucket(t) === filter; });
    var lanes = f.lanes.items.map(function (x) { return [x.name, x.task, x.step, x.reviewing]; });
    if (!changed("tasks", [rows, lanes, route.task])) return;
    var tb = clear($("task-rows"));
    rows.forEach(function (t) {
      var tr = el("tr", route.task === t.id ? "sel" : "");
      var td = el("td");
      var a = link("#work/" + encodeURIComponent(t.id), t.id);
      if (route.task === t.id) a.setAttribute("aria-current", "true");
      a.setAttribute("aria-label", t.id + ": " + t.title + ", open details");
      td.appendChild(a);
      tr.appendChild(td);
      tr.appendChild(el("td", "", t.title));
      var l = laneFor(f, t.id);
      tr.appendChild(el("td", "muted", [t.writer, l && l.lane.name].filter(Boolean).join(" · ") || "–"));
      var st = el("td");
      st.appendChild(stateWords(f, t));
      tr.appendChild(st);
      tb.appendChild(tr);
    });
    show("task-none", rows.length === 0);
    tickLanes();
  }
  function renderDrawer(f) {
    var id = route.name === "work" ? route.task : "";
    var t = null;
    f.tasks.forEach(function (x) { if (x.id === id) t = x; });
    var pt = null;
    if (!t && id && f.paused) f.paused.tasks.forEach(function (x) { if (x.title.indexOf(id + " ") === 0 || x.title === id) pt = x; });
    var dr = $("drawer");
    $("drawer").parentNode.className = "workgrid" + ((t || pt || id) ? " open" : "");
    if (!id) { dr.hidden = true; sigs.drawer = ""; return; }
    var l = t ? laneFor(f, t.id) : null, rl = t ? reviewLaneFor(f, t.id) : null;
    var rt = retryTask(f);
    if (!changed("drawer", [id, t, pt, l && [l.lane.name, l.lane.model, l.lane.step, l.lane.process, l.lane.note, l.lane.signal.state], rl && rl.lane.name, rt, armed, pending, f.act && f.act.card, !!f.paused, workCapabilities(f, id)])) return;
    dr.hidden = false;
    var facts = clear($("dr-facts")), acts = clear($("dr-acts"));
    setText("dr-note", "");
    if (!t && !pt) {
      setText("dr-title", id);
      clear($("dr-state")).textContent = "This task is not on the Board now.";
      renderWorkControls(f, id, acts);
      return;
    }
    if (pt) {
      setText("dr-title", pt.title);
      chip(clear($("dr-state")).appendChild(el("span")), "paused", "bad");
      kv(facts, "Progress", pt.progress);
      kv(facts, "Kept", pt.kept);
      setText("dr-note", "The choice for this task is on Home, in the paused card.");
      acts.appendChild(link("#home", "Go to the paused card"));
      renderWorkControls(f, id, acts);
      return;
    }
    setText("dr-title", t.id + " · " + t.title);
    var stBox = clear($("dr-state"));
    stBox.appendChild(stateWords(f, t));
    kv(facts, "Files", t.files);
    kv(facts, "Worker", [t.writer, l && l.lane.name, l && l.lane.model].filter(Boolean).join(" · ") || "not assigned yet");
    if (l) {
      kv(facts, "Step", l.lane.step);
      if (l.lane.process) kv(facts, "Worker process", l.lane.process + (l.lane.turn ? " · turn " + l.lane.turn.replace("_", " ") : ""));
      if (l.lane.note) kv(facts, "Liveness", l.lane.note);
      var run = el("span", "el"); run.dataset.lane = l.i; kv(facts, "Running for", run);
      var sig = el("span", "sig"); sig.dataset.lane = l.i; kv(facts, "Last signal", sig);
    }
    kv(facts, "Reviewer", [t.reviewer, rl && "reviewing now on " + rl.lane.name].filter(Boolean).join(" · "));
    var n = rework(t);
    kv(facts, "Attempt", n ? "attempt " + (n + 1) + ": the reviewer sent it back " + plural(n, "time") : t.state === "building" || t.state === "review" ? "first attempt" : "");
    if (n) kv(facts, "Why sent back", "The reviewer's reason is not sent to this page yet; the terminal Board's task inspector shows it.", "muted");
    if (t.note && !n) kv(facts, "Note", t.note);
    if (t.waiting) kv(facts, "Waits", waitText(t.waiting) + " (" + (t.waiting.code === "unknown" ? "cause unknown, from " : "from ") + t.waiting.source + ")");
    renderWorkControls(f, t.id, acts);
    tickLanes();
  }
  function renderPlan(f) {
    var p = f.plan;
    show("w-plan", !!(p.title || p.accepted || p.total));
    setText("pl-title", p.title);
    setText("pl-accepted", p.accepted ? cap(p.accepted) : "");
    var stats = [];
    if (p.mergedPerHr) stats.push(p.mergedPerHr + " merged per hour");
    if (p.reworked) stats.push(p.reworked + " sent back");
    if (p.tokens) stats.push(compact(p.tokens) + " tokens used");
    if (p.etaSec > 0) stats.push("≈ " + dur(p.etaSec) + " to go");
    setText("pl-stats", stats.join(" · "));
    if (changed("spark", p.throughput)) spark(p.throughput);
  }
  function spark(values) {
    var svg = $("pl-spark"), ns = "http://www.w3.org/2000/svg";
    clear(svg);
    if (!values || values.length < 2) { $("pl-spark-fig").hidden = true; return; }
    $("pl-spark-fig").hidden = false;
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
    var l = f.lanes;
    show("w-lanes", l.items.length > 0 || !!f.top.run);
    if (!changed("lanes", l.items.map(function (x) { return [x.name, x.model, x.provider, x.task, x.step, x.reviewing, x.selected, x.process, x.turn, x.note, x.signal, x.waiting]; }))) { tickLanes(); return; }
    var tb = clear($("la-rows"));
    l.items.forEach(function (x, i) {
      var tr = el("tr");
      tr.dataset.lane = i;
      tr.appendChild(el("td", "", x.name));
      tr.appendChild(el("td", "muted", x.model || x.provider));
      tr.appendChild(el("td", x.reviewing ? "tone-vio" : "", x.task));
      tr.appendChild(el("td", "", x.waiting ? x.step + " · " + waitText(x.waiting) : x.note ? x.step + " · " + x.note : x.step));
      var e = el("td", "num el"); e.dataset.lane = i; tr.appendChild(e);
      var s = el("td", "num sig"); s.dataset.lane = i; tr.appendChild(s);
      tb.appendChild(tr);
    });
    show("la-empty", l.items.length === 0);
    $("la-rows").closest(".tablewrap").hidden = l.items.length === 0;
    tickLanes();
  }
  // ---- liveness: pure helpers, read by testdata/liveness.cjs
  // signalView is a lane's last signal as the page says it, add seconds
  // after the frame came. Stale, unverifiable and exited are the server's
  // judgement only; unverifiable reads "cannot verify", never exited. The
  // local clock only turns a known signal amber after 60 s of quiet.
  function signalView(x, add) {
    var s = x.signal || {};
    switch (s.state) {
      case "stale": return { text: s.label || "no heartbeat: cannot verify the worker", tone: "bad" };
      case "unverifiable": return { text: /^cannot verify/.test(s.label || "") ? s.label : "cannot verify" + (s.label ? ": " + s.label : ""), tone: "warn" };
      case "exited": return { text: s.label || "the worker exited", tone: "muted" };
      case "live": case "quiet": break;
      default: return { text: s.label || "–", tone: "" };
    }
    if (!s.at) return { text: "–", tone: "" };
    var sec = x.lastSignalSec + add, quiet = s.state === "quiet" || sec > 60;
    return { text: dur(sec) + " ago" + (quiet ? " · quiet" : ""), tone: quiet ? "warn" : "" };
  }
  // waitText is a wait in one line: its label and where to look.
  function waitText(w) {
    if (!w) return "";
    var t = w.label || (w.code === "unknown" ? "waits, cause unknown" : w.code);
    if (w.retryAt) t += " · retry " + hm(w.retryAt);
    if (w.inspect) t += " · see " + w.inspect;
    return t;
  }
  // approvalAge is an approval's age and time left, add seconds after a
  // frame made at nowIso.
  function approvalAge(a, nowIso, add) {
    var age = (a.ageSec || 0) + add, out = "waiting " + dur(age);
    if (a.status !== "requested") return out + " · " + a.status + ", cannot be answered";
    var exp = parse(a.expiresAt), now = parse(nowIso);
    if (exp && now) {
      var left = (exp - now) / 1000 - add;
      out += left > 0 ? " · expires in " + dur(left) : " · expired, cannot be answered";
    }
    return out;
  }
  // ---- end liveness

  // tickLanes moves every running clock on the page by the time since the
  // frame came.
  function tickLanes() {
    if (!frame) return;
    var add = since();
    Array.prototype.forEach.call(document.querySelectorAll("[data-lane]"), function (n) {
      var x = frame.lanes.items[+n.dataset.lane];
      if (!x) return;
      var sv = signalView(x, add);
      if (n.tagName === "TR") { n.classList.toggle("stale", sv.tone === "bad"); n.classList.toggle("quiet", sv.tone === "warn"); return; }
      if (n.classList.contains("el")) n.textContent = dur(x.elapsedSec + add);
      else if (n.classList.contains("sig")) { n.textContent = sv.text; n.className = n.className.replace(/ ?tone-\w+/g, "") + (sv.tone ? " " + toneClass(sv.tone) : ""); }
    });
    Array.prototype.forEach.call(document.querySelectorAll("[data-approval-age]"), function (n) {
      var a = null;
      (frame.approvals || []).forEach(function (x) { if (x.id === n.dataset.approvalAge) a = x; });
      if (a) n.textContent = approvalAge(a, frame.now, add);
    });
  }

  // renderInbox draws what waits for the owner: the run's wait, the inbox
  // lines and the approvals. Approvals are keyed by id and updated in
  // place, so a focused button or its age survives a new frame.
  function renderInbox(f) {
    var ib = f.inbox || { lines: [] }, aps = f.approvals || [];
    setText("ib-count", (ib.count || 0) + aps.filter(function (a) { return a.status === "requested"; }).length || "");
    var rw = $("run-wait");
    rw.hidden = !f.waiting;
    rw.textContent = f.waiting ? "The run waits: " + waitText(f.waiting) : "";
    if (changed("inbox", [ib.lines, ib.next])) {
      var ul = clear($("ib-lines"));
      ib.lines.forEach(function (l) { ul.appendChild(el("li", "", l)); });
      setText("ib-next", ib.next ? "Next ask: " + ib.next : "");
    }
    show("ib-empty", ib.lines.length === 0 && aps.length === 0 && !f.waiting && f.nowLine.fromYouOK);
    // Rows that left go first, so the rows that stay are not moved and
    // keep their focus.
    var box = $("ib-approvals"), have = {}, ids = {};
    aps.forEach(function (a) { ids[a.id] = true; });
    Array.prototype.slice.call(box.children).forEach(function (n) {
      if (ids[n.dataset.id]) have[n.dataset.id] = n; else box.removeChild(n);
    });
    aps.forEach(function (a, i) {
      var li = have[a.id];
      if (!li) {
        li = el("li", "approval");
        li.dataset.id = a.id;
        li.appendChild(el("b", "ap-title"));
        li.appendChild(el("span", "muted ap-age")).dataset.approvalAge = a.id;
        li.appendChild(el("p", "ap-wait"));
        li.appendChild(el("p", "muted ap-reason"));
        li.appendChild(el("span", "acts ap-acts"));
      }
      if (box.children[i] !== li) box.insertBefore(li, box.children[i] || null);
      li.querySelector(".ap-title").textContent = (a.taskId ? a.taskId + ": " : "") + "approval " + a.id + " · " + (a.awaiting === "owner" ? "you answer" : "the Marshal answers") + " ";
      li.querySelector(".ap-wait").textContent = a.waiting ? waitText(a.waiting) : "";
      li.querySelector(".ap-reason").textContent = a.reason || "";
      var acts = li.querySelector(".ap-acts"), sig = JSON.stringify([a.status, a.actions, a.destination]);
      if (acts.dataset.sig !== sig) {
        acts.dataset.sig = sig;
        clear(acts);
        if (a.actions.length) {
          a.actions.forEach(function (k) {
            acts.appendChild(button("ghost", "Copy " + k.label + " command", function () {
              if (navigator.clipboard) navigator.clipboard.writeText(k.key);
              feedback("ok", "Copied: " + k.key + " (run it in the terminal).");
            }));
          });
        } else if (a.status === "requested") {
          acts.appendChild(el("span", "muted", "Answered at " + (a.destination || "the Marshal") + "."));
        }
      }
    });
  }

  // ---- Architecture ------------------------------------------------------------
  function renderArchitecture(f) {
    var d = f.decision && f.decision.kind === "architecture" ? f.decision : null;
    var arch = null;
    f.plan.stages.forEach(function (s) { if (s.name === "arch") arch = s; });
    var rev = "";
    var m = /rev(?:ision)?\s*(\d+)/i.exec((d && d.title) || (arch && arch.note) || f.plan.accepted || "");
    if (m) rev = "revision " + m[1];
    var pendingReq = f.requests.filter(function (r) { return r.answer === "pending"; }).length;
    var canOpen = !!(d && f.act && f.act.tokens.a);
    if (!changed("arch", [d, arch, f.plan.accepted, pendingReq, canOpen, pending, f.screen])) return;
    var dl = clear($("ar-facts"));
    kv(dl, "Revision", rev || "no architecture yet");
    var status = d ? "proposed: waiting for your Accept on the review page" :
      f.plan.accepted ? cap(f.plan.accepted) : arch && arch.state === "done" ? "accepted" : arch && arch.state === "current" ? "being drafted by the Marshal" : "not drafted yet";
    kv(dl, "State", status, d ? "tone-warn" : "");
    if (d) kv(dl, "Proposal", [d.summary, d.changedNote].filter(Boolean).join(" · "));
    kv(dl, "Open requests to the Marshal", String(pendingReq));
    var acts = clear($("ar-acts"));
    if (canOpen) {
      acts.appendChild(button("primary", "Open the architecture map", function () { act({ action: "key", key: "a" }, "Open the architecture map"); }));
      setText("ar-note", "The map opens on its own review page in a new tab; Accept happens only there, never on this page.");
    } else {
      setText("ar-note", d ? "The review page opens from the card on Home." :
        "The architecture map opens from here while the Marshal proposes a revision. Accept happens only on its review page.");
    }
    var parts = d ? d.nodes : [];
    show("ar-parts", parts.length > 0);
    var ol = clear($("ar-nodes"));
    parts.forEach(function (n) {
      var li = el("li", n.changed ? "changed" : "", n.name);
      if (n.sub) li.appendChild(el("small", "", n.sub));
      if (n.changed) li.appendChild(el("small", "tone-info", "changed by this goal"));
      ol.appendChild(li);
    });
  }

  // ---- Team ------------------------------------------------------------------
  var windowName = { "5h": "5-hour window", "weekly": "weekly window" };
  function limitRows(rows, who) {
    var ul = el("ul", "limits");
    rows.forEach(function (r) {
      var li = el("li");
      li.appendChild(el("span", "what", [r.family || "all models", windowName[r.window] || r.window].filter(Boolean).join(" · ")));
      if (r.percentLeft >= 0) {
        li.appendChild(el("span", "num " + toneClass(leftTone(r.percentLeft)), r.percentLeft + "% left"));
        li.appendChild(newBar(who + " " + (r.family || "") + " " + (windowName[r.window] || r.window) + " left", r.percentLeft, leftTone(r.percentLeft)));
      } else {
        li.appendChild(el("span", "muted", /^not /.test(r.source || "") ? r.source : "not reported by the CLI"));
      }
      var w = [];
      if (r.resetsAt) w.push("resets " + when(r.resetsAt));
      if (r.observedAt) w.push("read " + ago(r.observedAt));
      if (r.source && !/^not /.test(r.source)) w.push("from " + r.source);
      if (r.drives) w.push("decides dispatch");
      if (w.length) li.appendChild(el("span", "when", w.join(" · ")));
      ul.appendChild(li);
    });
    return ul;
  }
  function renderTeam(f) {
    var m = f.marshal;
    if (!changed("team", [m, f.workers, f.limits, f.lanes.limits, f.ready, f.chat, Math.floor(Date.now() / 60000)])) return;
    var limitsOf = {};
    (f.limits || []).forEach(function (p) { limitsOf[p.provider] = p; });
    var ul = clear($("team-cards"));
    setText("team-sum", f.workers.length ? plural(f.workers.length, "provider") : "");
    if (m.provider) {
      var li = el("li"), head = el("div", "tc-head");
      head.appendChild(el("b", "", m.provider));
      head.appendChild(el("span", "tc-role", "Marshal"));
      var c = el("span"); chip(c, m.inUse ? "in use" : "idle", m.inUse ? "ok" : ""); head.appendChild(c);
      li.appendChild(head);
      li.appendChild(el("p", "muted", [m.model, m.effort && m.effort + " effort"].filter(Boolean).join(" · ")));
      if (m.doing) li.appendChild(el("p", "", cap(m.doing)));
      var rows = [];
      if (m.limit5hPct >= 0) rows.push({ family: m.model, window: "5h", percentLeft: m.limit5hPct });
      if (m.weeklyPct >= 0) rows.push({ family: m.model, window: "weekly", percentLeft: m.weeklyPct });
      if (rows.length) li.appendChild(limitRows(rows, m.provider));
      else li.appendChild(el("p", "muted", "Limits: not reported by the CLI"));
      if (m.contextPct >= 0) {
        var cl = el("ul", "limits"), ci = el("li");
        ci.appendChild(el("span", "what", "context used"));
        ci.appendChild(el("span", "num", m.contextPct + "%" + (m.contextWindow ? " of " + compact(m.contextWindow) : "")));
        ci.appendChild(newBar("Marshal context used", m.contextPct, m.contextPct >= 85 ? "bad" : m.contextPct >= 65 ? "warn" : ""));
        cl.appendChild(ci); li.appendChild(cl);
      }
      var bound = m.chatBound || f.chat.bound;
      li.appendChild(el("p", bound ? "muted" : "reason", bound ? "Chat bound" + (f.chat.restarts ? " · " + plural(f.chat.restarts, "restart") : "") : "⚠ Chat not bound"));
      ul.appendChild(li);
    }
    f.workers.forEach(function (w) {
      var li = el("li"), head = el("div", "tc-head");
      head.appendChild(el("b", "", (w.glyph ? w.glyph + " " : "") + w.provider));
      head.appendChild(el("span", "tc-role", "Worker"));
      var c = el("span"); chip(c, w.tag || (w.signedIn ? "signed in" : w.installed ? "not signed in" : "not installed"), w.tagTone); head.appendChild(c);
      li.appendChild(head);
      var state = [w.detail, !w.installed ? "not installed" : !w.signedIn ? "not signed in" : ""].filter(Boolean).join(" · ");
      if (state) li.appendChild(el("p", "muted", state));
      var pl = limitsOf[w.provider];
      if (pl && pl.state) {
        var dp = el("p");
        dp.appendChild(el("b", "", cap(pl.state)));
        var why = [pl.model && "runs " + pl.model, pl.reason].filter(Boolean).join(" · ");
        if (why) dp.appendChild(document.createTextNode(": " + why));
        li.appendChild(dp);
      }
      if (pl && pl.rows && pl.rows.length) li.appendChild(limitRows(pl.rows, w.provider));
      else li.appendChild(el("p", "muted", "Limits: not reported yet"));
      if (w.reason) li.appendChild(el("p", "reason", "⚠ " + w.reason));
      ul.appendChild(li);
    });
    if (!ul.firstChild) {
      var e = el("li", "empty");
      e.appendChild(el("b", "", "No provider reported yet"));
      e.appendChild(el("span", "", "The team appears once the Board has checked which providers are installed and signed in."));
      ul.appendChild(e);
    }
    var dl = clear($("tm-defaults"));
    kv(dl, "Lane caps", f.lanes.limits ? f.lanes.limits.replace(/^limit /, "") : "");
    f.ready.defaults.forEach(function (d) { kv(dl, cap(d.name), d.value); });
    show("tm-rules", !!dl.firstChild);
    var r = f.ready;
    show("tm-ready", r.checks.length > 0);
    setText("r-count", r.total ? "· " + r.ready + " of " + r.total + " ready" : "");
    var cl2 = clear($("r-checks"));
    var marks = { ok: ["✓ ok", "ok"], warn: ["! check", "warn"], bad: ["✗ fix", "bad"] };
    r.checks.forEach(function (c) {
      var mk = marks[c.mark] || ["·", ""];
      var li = el("li");
      var ch = el("span"); chip(ch, mk[0], mk[1]); li.appendChild(ch);
      li.appendChild(el("b", "", c.label));
      li.appendChild(el("span", "", c.text));
      li.appendChild(el("span", "muted", c.tag));
      if (c.hint) li.appendChild(el("span", "hint", c.hint));
      cl2.appendChild(li);
    });
    setText("r-checked", r.checked ? "Checked " + when(r.checked) + ". Check again from the terminal Board." : "");
  }

  // ---- Activity ------------------------------------------------------------------
  var tab = "all";
  function renderActivity(f) {
    // Requests that expired or were refused are told in Activity too, in
    // plain words.
    var settled = f.requests.filter(function (r) { return r.answer === "expired" || r.answer === "decline"; }).map(function (r) {
      return { at: r.sent, kind: "request", tone: "warn", count: 0,
        text: "The request to " + plainText(r.what) + (r.answer === "expired" ? " expired before it was answered" : " was refused") };
    });
    var all = f.activity.concat(settled).sort(function (a, b) { return (parse(b.at) || 0) - (parse(a.at) || 0); });
    if (changed("activity", all)) {
      var box = clear($("act-groups")), group = null, key = "";
      all.forEach(function (e) {
        var d = parse(e.at);
        var k = d ? when(e.at).replace(/\d\d:\d\d$/, "") + pad(d.getHours()) + ":00" : "Earlier";
        if (k !== key) {
          key = k;
          var g = el("section", "group");
          g.appendChild(el("h3", "", k === "Earlier" ? k : "From " + k));
          group = el("ol", "events");
          g.appendChild(group);
          box.appendChild(g);
        }
        group.appendChild(eventRow(e));
      });
      show("act-empty", all.length === 0);
    }
    if (changed("steps", [f.steps.items, f.steps.model])) {
      setText("st-model", f.steps.model ? "Model: " + f.steps.model : "");
      var ol = clear($("st-rows"));
      f.steps.items.forEach(function (x) {
        ol.appendChild(eventRow({ at: x.at, kind: x.kind, text: x.text, tone: "info" }));
      });
      show("st-empty", f.steps.items.length === 0);
    }
    setText("st-quiet", f.steps.quietSec ? "Last tool call " + dur(f.steps.quietSec + since()) + " ago" : "");
    if (changed("requests", f.requests)) {
      var tb = clear($("rq-rows"));
      f.requests.forEach(function (r) {
        var tr = el("tr");
        tr.appendChild(el("td", "num", hm(r.sent)));
        tr.appendChild(el("td", "", plainText(r.what)));
        var td = el("td"), c = el("span");
        var words = { allow: "allowed", decline: "refused", expired: "expired before it was answered", pending: "waiting for an answer" };
        chip(c, words[r.answer] || r.answer || "–", r.tone || (r.answer === "pending" || r.answer === "expired" ? "warn" : ""));
        td.appendChild(c);
        tr.appendChild(td);
        tr.appendChild(el("td", "num", r.latencySec ? dur(r.latencySec) : "–"));
        tb.appendChild(tr);
      });
      show("rq-empty", f.requests.length === 0);
      $("rq-rows").closest(".tablewrap").hidden = f.requests.length === 0;
    }
    var c = f.chat, notes = [];
    if (c.answered) notes.push(plural(c.answered, "request") + " answered");
    if (c.resent) notes.push(c.resent + " resent");
    if (c.restarts) notes.push(plural(c.restarts, "chat restart") + (c.reboundAt ? ", rebound " + hm(c.reboundAt) : ""));
    setText("rq-chat", notes.join(" · "));
  }
  function selectTab(name, focus) {
    tab = name;
    [["all", "tab-all", "tp-all"], ["steps", "tab-steps", "tp-steps"], ["req", "tab-req", "tp-req"]].forEach(function (x) {
      var on = x[0] === name;
      $(x[1]).setAttribute("aria-selected", String(on));
      $(x[1]).tabIndex = on ? 0 : -1;
      $(x[2]).hidden = !on;
      if (on && focus) $(x[1]).focus();
    });
  }

  // ---- Settings ------------------------------------------------------------------
  function renderSettings(f) {
    var r = f.ready;
    var s = f.system || {};
    if (!changed("settings", [f.top.version, f.top.project, f.top.run, r.telemetryAsk, r.telemetryOn, r.defaults, r.sessionLabel, f.plan.tokens, f.v, s, f.settings, ((f.settingsActions || {}).capabilities || []).map(function (c) { return [c.op, c.target, c.revision]; })])) return;
    var dl = clear($("set-facts"));
    kv(dl, "Version", (f.top.version || "unknown") + (s.updateAvailable ? " · " + s.updateAvailable + " is available (F10 or /update in the terminal)" : ""),
      s.updateAvailable ? "tone-warn" : "");
    var pct = function (n, what) { return n >= 0 ? n + "% " + what : what + " unknown"; };
    var host = [pct(s.cpuPct, "CPU"), pct(s.memPct, "memory used"), pct(s.diskPct, "disk used") + (s.lowDisk ? " (low on space)" : ""),
      s.tempC >= 0 ? s.tempC + " °C" : ""].filter(Boolean).join(" · ");
    kv(dl, "Host load", host);
    kv(dl, "Thermal cap", s.thermalGuard ? "on: the host runs hot" + (s.thermalCap ? "; temporary lane cap " + s.thermalCap + " for new lanes" : "") : "off", s.thermalGuard ? "tone-warn" : "");
    if (s.notice) kv(dl, "Host note", s.notice);
    kv(dl, "MCP", s.mcpFailed ? "failed in this session · F6 in the terminal shows why" : "working", s.mcpFailed ? "tone-bad" : "");
    var approval = "";
    r.defaults.forEach(function (d) { if (d.name === "approval") approval = d.value; });
    kv(dl, "Approval mode", approval || "not reported to this page yet");
    kv(dl, "Session label", (r.sessionLabel || "unknown") + " (supervision label)");
    kv(dl, "Project", f.top.project || "–");
    kv(dl, "Run", f.top.run || "no run");
    // Telemetry is known once the Board probed this computer (the checks
    // and the defaults come with it).
    var probed = r.checks.length > 0 || r.defaults.length > 0;
    kv(dl, "Usage counts (telemetry)", probed ? (r.telemetryOn ? "on · marshal telemetry disable turns it off" : "off") : "not checked yet · marshal telemetry status");
    if (f.plan.tokens) kv(dl, "Tokens this run", compact(f.plan.tokens));
    var settings = f.settings || {rows: [], changes: []};
    kv(dl, "Project settings revision", String(settings.revision || 0));
    var about = false;
    var controls = clear($("settings-controls"));
    ((f.settingsActions || {}).capabilities || []).forEach(function (cap) {
      if (cap.op !== "settings/ultra.request") return;
      var requestButton = el("button", "", "Request ULTRA entitlement"); requestButton.type = "button";
      requestButton.onclick = function () { previewSetting(newSettingRequest(cap, "")); };
      controls.appendChild(requestButton);
    });
    (settings.rows || []).forEach(function (row) {
      if (row.scope === "build" && !about) { kv(dl, "About this build", "Fixed edition policies"); about = true; }
      kv(dl, row.key, row.effective + (row.units ? " " + row.units : "") +
        " · " + row.scope + " · " + row.source + " · " + row.applies +
        (row.value !== row.effective ? " · configured: " + row.value : "") +
        (row.lockedReason ? " · locked: " + row.lockedReason : "") +
        (row.help ? " · " + row.help : ""));
      var cap = settingCapability(f, row.key);
      if (cap && !row.lockedReason) {
        var group = el("div", "setting-control");
        var label = el("label", "", row.key + " ");
        var input = el(cap.values && cap.values.length ? "select" : "input");
        input.setAttribute("aria-label", row.key);
        if (cap.values && cap.values.length) cap.values.forEach(function (value) { var option = el("option", "", value); option.value = value; input.appendChild(option); });
        input.value = row.key === "ultra-execution" ? (row.next === "on" || row.next === "true" ? "on" : "off") : row.value;
        label.appendChild(input); group.appendChild(label);
        var button = el("button", "", "Preview change"); button.type = "button";
        button.onclick = function () { previewSetting(newSettingRequest(cap, input.value)); };
        group.appendChild(button); controls.appendChild(group);
      }
    });
    kv(dl, "Budget usage", "Unknown usage is not zero and does not bypass quota. Run ceilings use tokens, minor currency units and seconds. Goal ceilings belong to the approved goal revision.");
    (settings.changes || []).forEach(function (c) {
      kv(dl, "Change r" + c.revision, c.key + ": " + c.old + " → " + c.new + " · " + c.actor + " · " + c.at);
    });
    kv(dl, "This page", "private link for this computer only · frame format " + f.v);
  }

  var settingStorage = "marshal-board-setting-request";
  var settingReceiptTimer = null;
  function workCapabilities(f, id) {
    return ((f.settingsActions || {}).capabilities || []).filter(function (c) {
      return c.target.kind === "task" && c.target.id === id || c.target.kind === "lane" && c.taskId === id;
    });
  }
  function newWorkRequest(capability, payload) {
    var body = newSettingRequest(capability, "");
    body.runId = capability.runId; body.attemptId = capability.attemptId; body.payload = payload;
    return body;
  }
  function renderWorkControls(f, id, box) {
    var labels = {"task/retry": "Retry", "task/retry.debug": "Retry in a fresh session with diagnostics", "task/reassign": "Reassign", "task/model.set": "Change task model", "task/skip": "Skip and require a revised plan", "task/accept": "Accept task", "task/return": "Return for rework", "task/approve-task": "Approve task", "lane/nudge": "Request worker status", "lane/restart": "Restart this worker"};
    workCapabilities(f, id).forEach(function (c) {
      var row = el("div", "acts"), provider, reason;
      if (c.op === "task/reassign" || c.op === "task/model.set") {
        provider = el("input"); provider.placeholder = "Provider"; provider.setAttribute("aria-label", "Task provider"); row.appendChild(provider);
      }
      if (c.op === "task/return" || c.op === "task/reassign") {
        reason = el("input"); reason.placeholder = "Reason"; reason.maxLength = 2000; reason.setAttribute("aria-label", "Action reason"); row.appendChild(reason);
      }
      var button = el("button", "ghost", labels[c.op]); button.type = "button";
      button.onclick = function () {
        var payload = {};
        if (provider) payload.provider = provider.value.trim();
        if (reason) payload.reason = reason.value.trim();
        if (c.approvalId) payload.approvalId = c.approvalId;
        previewSetting(newWorkRequest(c, payload));
      };
      row.appendChild(button); box.appendChild(row);
    });
  }
  function runCapabilities(f) {
    return ((f.settingsActions || {}).capabilities || []).filter(function (c) { return c.target.kind === "run"; });
  }
  // Run-wide controls sit above every route, so they stay reachable with a
  // card or a drawer open; each one previews and confirms its bound request.
  function renderRunControls(f) {
    var labels = {"run/pause": "Pause the run", "run/stop": "Stop the run", "run/resume": "Resume the run", "run/stop-workers": "Stop the run's workers", "run/amend.deny": "Deny the amendment"};
    var caps = runCapabilities(f), box = $("run-controls");
    if (!changed("run-controls", caps.map(function (c) { return [c.op, c.revision, c.attemptId]; }))) return;
    clear(box); box.hidden = !caps.length;
    if (!caps.length) return;
    var reason = el("input"); reason.placeholder = "Reason (optional)"; reason.maxLength = 2000; reason.setAttribute("aria-label", "Run action reason");
    box.appendChild(reason);
    caps.forEach(function (c) {
      var b = el("button", c.op === "run/resume" ? "ghost" : "ghost danger", labels[c.op] || c.op); b.type = "button";
      b.onclick = function () { var payload = {}; if (reason.value.trim()) payload.reason = reason.value.trim(); previewSetting(newWorkRequest(c, payload)); };
      box.appendChild(b);
    });
  }
  function commandPanel(body) { return $(body && body.target.kind === "run" ? "run-confirm" : body && (body.target.kind === "task" || body.target.kind === "lane") ? "work-confirm" : "settings-confirm"); }
  function settingCapability(f, key) {
    return ((f.settingsActions || {}).capabilities || []).find(function (c) { return c.target.id === key && c.target.kind !== "task" && c.target.kind !== "lane" && c.target.kind !== "run"; });
  }
  function newSettingRequest(cap, value) {
    var bytes = new Uint8Array(16); crypto.getRandomValues(bytes);
    var id = Array.from(bytes).map(function (b) { return b.toString(16).padStart(2, "0"); }).join("");
    return {action: "cmd", requestId: id, target: cap.target, op: cap.op, projectId: frame.settingsActions.projectId,
      revision: cap.revision, payload: cap.op === "settings/ultra.request" ? {} : cap.op === "settings/ultra.set" ? {enabled: value === "on"} : {key: cap.target.id, value: value}};
  }
  function rememberSetting(body) {
    var saved = Object.assign({}, body); delete saved.token; delete saved.preview;
    sessionStorage.setItem(settingStorage, JSON.stringify(saved));
  }
  function settingPost(body) {
    var cap = ((frame.settingsActions || {}).capabilities || []).find(function (c) { return c.target.id === body.target.id && c.target.kind === body.target.kind && c.op === body.op; });
    if (!cap || cap.op !== body.op || cap.revision !== body.revision || (cap.runId || "") !== (body.runId || "") || (cap.attemptId || "") !== (body.attemptId || "") || frame.settingsActions.projectId !== body.projectId) return Promise.reject(new Error("The project or settings changed. Refresh and preview again."));
    var headers = auth(); headers["Content-Type"] = "application/json";
    return fetch("/api/act", {method: "POST", headers: headers, credentials: "omit", cache: "no-store",
      body: JSON.stringify(Object.assign({}, body, {token: cap.token}))}).then(function (r) {
      return r.json().then(function (result) { if (!r.ok) throw new Error(result.text || "The change was refused."); return result; });
    });
  }
  function settingReceipt(body) {
    return fetch("/api/act/receipts/" + body.requestId, {headers: auth(), credentials: "omit", cache: "no-store"}).then(function (r) {
      if (r.status === 404) return null;
      if (!r.ok) throw new Error("Reopen the Board to check this request.");
      return r.json();
    });
  }
  function showSettingReceipt(r) {
    var body; try { body = JSON.parse(sessionStorage.getItem(settingStorage)); } catch (e) {}
    feedback(r.status === "applied" ? "ok" : r.status === "queued" || r.status === "applying" ? "wait" : "bad",
      r.status + ": " + (r.result || r.code) + " · revision " + r.revision);
    clearTimeout(settingReceiptTimer);
    if (r.status === "queued" || r.status === "applying") settingReceiptTimer = setTimeout(recoverSetting, 1000);
    if (r.status === "applied" || r.status === "refused") { sessionStorage.removeItem(settingStorage + "-submitted"); sessionStorage.removeItem(settingStorage); clear(commandPanel(body)); }
    if (r.status === "interrupted") {
      var panel = clear(commandPanel(body));
      var check = el("button", "", "Check previous outcome"); check.type = "button"; check.onclick = recoverSetting; panel.appendChild(check);
    }
    return refresh();
  }
  function previewSetting(body) {
    var unresolved = sessionStorage.getItem(settingStorage + "-submitted");
    if (unresolved && unresolved !== body.requestId) { feedback("bad", "Check the previous settings receipt before starting another change."); recoverSetting(); return; }
    try { rememberSetting(body); } catch (e) { feedback("bad", "Browser storage is unavailable; enable it before changing settings."); return; }
    feedback("wait", "Preparing confirmation…");
    settingReceipt(body).then(function (r) {
      if (r) return showSettingReceipt(r);
      return refresh().then(function () { return settingPost(Object.assign({}, body, {preview: true, confirmation: undefined})); }).then(function (r) {
        body.confirmation = r.confirmation; rememberSetting(body);
        var panel = clear(commandPanel(body));
        var work = body.target.kind === "task" || body.target.kind === "lane" || body.target.kind === "run";
        var detail = work ? JSON.stringify(body.payload) : (body.payload.value === undefined ? (body.payload.enabled ? "on" : "off") : body.payload.value);
        panel.appendChild(el("p", "", "Confirm " + body.op + " for " + body.target.id + ": " + detail + ". This preview expires in one minute."));
        var button = el("button", "", "Confirm change"); button.type = "button"; panel.appendChild(button);
        button.onclick = function () {
          button.disabled = true;
          sessionStorage.setItem(settingStorage + "-submitted", body.requestId);
          refresh().then(function () { return settingPost(body); }).then(showSettingReceipt).catch(function (e) {
            feedback("wait", "Checking the receipt…");
            settingReceipt(body).then(function (r) { if (r) return showSettingReceipt(r); feedback("bad", e.message + " Preview again to retry this same request."); }).catch(function () { feedback("bad", "Outcome unknown. Check this request before retrying."); });
          });
        };
        feedback("ok", "Preview ready; no change has been made.");
      });
    }).catch(function (e) { feedback("bad", e.message); });
  }
  function recoverSetting() {
    var body; try { body = JSON.parse(sessionStorage.getItem(settingStorage)); } catch (e) { return; }
    if (!body) return;
    settingReceipt(body).then(function (r) {
      if (r) { showSettingReceipt(r); return; }
      var panel = clear(commandPanel(body));
      panel.appendChild(el("p", "", "A previous settings change has no receipt. Preview it again to retry with the same request identity."));
      var button = el("button", "", "Preview previous change"); button.type = "button"; button.onclick = function () { previewSetting(body); }; panel.appendChild(button);
    }).catch(function () { feedback("bad", "A previous settings outcome is unknown; reconnect to check its receipt."); });
  }

  // ---- render ------------------------------------------------------------------
  var settingsRecovered = false;
  function render(f) {
    if (!f) return;
    if (!settingsRecovered) { settingsRecovered = true; recoverSetting(); }
    renderTop(f); renderCard(f); renderInbox(f); renderHome(f); renderWork(f); renderArchitecture(f);
    renderTeam(f); renderActivity(f); renderSettings(f); renderRunControls(f);
  }

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
    var tok = frame.act.tokens[name];
    if (!tok) { feedback("bad", "Refused: that button is not on the Board now."); return Promise.resolve(); }
    body.card = frame.act.card;
    body.token = tok;
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

  function tick() {
    var now = new Date();
    var c = $("t-clock");
    c.textContent = pad(now.getHours()) + ":" + pad(now.getMinutes());
    c.setAttribute("datetime", now.toISOString());
    tickLanes();
  }

  function live(state, text) {
    chip($("t-live"), text, state === "live" ? "ok" : state === "down" ? "bad" : "warn");
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
    function armWatch() {
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
      armWatch();
      function pump() {
        return reader.read().then(function (res) {
          if (res.done) { again("The Board closed the connection."); return; }
          armWatch();
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
    $("docs-link").href = docsURL;
    applyRoute(false);
    window.addEventListener("hashchange", function () { applyRoute(true); });
    // The pill leads to what waits: the card's main button on Home.
    $("you-pill").addEventListener("click", function () {
      setTimeout(function () {
        var p = $("primary");
        if (!$("card").hidden && !p.hidden) p.focus();
      }, 0);
    });
    ["all", "steps", "req"].forEach(function (name, i, all) {
      var id = { all: "tab-all", steps: "tab-steps", req: "tab-req" }[name];
      $(id).addEventListener("click", function () { selectTab(name, false); });
      $(id).addEventListener("keydown", function (e) {
        if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
        e.preventDefault();
        selectTab(all[(i + (e.key === "ArrowRight" ? 1 : all.length - 1)) % all.length], true);
      });
    });
    tick();
    setInterval(tick, 1000);
    $("say-send").addEventListener("click", send);
    $("say-text").addEventListener("keydown", function (e) {
      if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); send(); }
    });
    spendLink(connect);
  });
})();
