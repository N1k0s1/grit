// grit dashboard: renders profiles, timelines and live updates. No framework.
(function () {
  "use strict";

  function dur(sec) {
    sec = Math.max(0, Math.round(sec));
    if (sec < 60) return sec + "s";
    var h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60);
    return h ? h + "h " + String(m).padStart(2, "0") + "m" : m + "m";
  }
  function esc(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }
  function data() {
    var el = document.getElementById("data");
    return el ? JSON.parse(el.textContent) : null;
  }
  function dayKey(d) { return d.getFullYear() + "-" + (d.getMonth() + 1) + "-" + d.getDate(); }
  function ago(unix) {
    var s = Date.now() / 1000 - unix;
    if (s < 90) return "just now";
    if (s < 3600) return Math.round(s / 60) + "m ago";
    if (s < 86400) return Math.round(s / 3600) + "h ago";
    return Math.round(s / 86400) + "d ago";
  }

  // ---------- timeline ----------
  var NS = "http://www.w3.org/2000/svg";
  function el(name, attrs, parent) {
    var e = document.createElementNS(NS, name);
    for (var k in attrs) e.setAttribute(k, attrs[k]);
    if (parent) parent.appendChild(e);
    return e;
  }
  function timeline(host, s) {
    var t0 = new Date(s.start).getTime(), t1 = new Date(s.end).getTime();
    (s.intervals || []).forEach(function (iv) { t0 = Math.min(t0, new Date(iv.s).getTime()); t1 = Math.max(t1, new Date(iv.e).getTime()); });
    var span = Math.max(t1 - t0, 60000);
    var W = 1000, L = 64, R = 12, H = 92;
    var x = function (t) { return L + ((t - t0) / span) * (W - L - R); };
    var svg = el("svg", { viewBox: "0 0 " + W + " " + H, role: "img", "aria-label": "Session timeline" }, host);
    var style = getComputedStyle(document.documentElement);
    var accent = style.getPropertyValue("--accent").trim();
    var agent = style.getPropertyValue("--agent").trim();
    var fg = style.getPropertyValue("--fg").trim();

    // grid + axis
    var step = [60e3, 5 * 60e3, 15 * 60e3, 30 * 60e3, 3600e3, 2 * 3600e3, 6 * 3600e3].find(function (st) { return span / st <= 8; }) || 12 * 3600e3;
    for (var g = Math.ceil(t0 / step) * step; g <= t1; g += step) {
      el("line", { x1: x(g), x2: x(g), y1: 6, y2: 70, class: "grid" }, svg);
      var lbl = el("text", { x: x(g), y: 86, "text-anchor": "middle", class: "axis" }, svg);
      lbl.textContent = new Date(g).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
    }
    var l1 = el("text", { x: 0, y: 26, class: "lane-label" }, svg); l1.textContent = "you";
    var l2 = el("text", { x: 0, y: 58, class: "lane-label" }, svg); l2.textContent = "agent";

    (s.turns || []).forEach(function (tn) {
      var a = new Date(tn.at).getTime(), b = new Date(tn.end).getTime();
      var r = el("rect", { x: x(a), y: 46, width: Math.max(1.5, x(b) - x(a)), height: 18, rx: 3, fill: agent }, svg);
      el("title", {}, r).textContent = "Agent ran " + dur(tn.agent) + ", credited " + dur(tn.agent_credit) + ". " + tn.agent_rule;
      var tick = el("rect", { x: x(a) - 1, y: 8, width: 2, height: 58, fill: fg, opacity: 0.55 }, svg);
      el("title", {}, tick).textContent = "Prompt #" + tn.i + " (" + tn.chars + " chars). " + tn.think_rule;
    });
    (s.intervals || []).forEach(function (iv) {
      var a = new Date(iv.s).getTime(), b = new Date(iv.e).getTime();
      var r = el("rect", { x: x(a), y: 14, width: Math.max(1.5, x(b) - x(a)), height: 18, rx: 3, fill: accent }, svg);
      el("title", {}, r).textContent = iv.k + ": " + dur((b - a) / 1000);
    });
  }

  // ---------- profile ----------
  function renderProfile(p) {
    var now = new Date(), todayKey = dayKey(now);
    var days = {};
    var week = 0, weekWall = 0, all = 0, today = 0;
    var weekAgo = now.getTime() / 1000 - 7 * 86400;
    var harness = {};
    var tiers = { high: 0, medium: 0, low: 0 }, tierTotal = 0;
    p.sessions.forEach(function (s) {
      var d = new Date(s.start * 1000), k = dayKey(d);
      days[k] = days[k] || { you: 0, agent: 0 };
      days[k].you += s.credited; days[k].agent += s.agent;
      all += s.credited;
      if (k === todayKey) today += s.credited;
      if (s.start >= weekAgo) { week += s.credited; weekWall += s.wall; harness[s.harness] = (harness[s.harness] || 0) + s.credited; }
      if (s.tier in tiers) { tiers[s.tier] += s.credited; tierTotal += s.credited; }
    });
    // streak: consecutive days with at least 15 credited minutes, ending today or yesterday
    var streak = 0, cur = new Date(now);
    if (!(days[dayKey(cur)] && days[dayKey(cur)].you >= 900)) cur.setDate(cur.getDate() - 1);
    while (days[dayKey(cur)] && days[dayKey(cur)].you >= 900) { streak++; cur.setDate(cur.getDate() - 1); }

    document.getElementById("kpis").innerHTML =
      '<div class="stat accent"><b>' + dur(today) + "</b><span>today</span></div>" +
      "<div class=stat><b>" + dur(week) + "</b><span>last 7 days</span></div>" +
      "<div class=stat><b>" + (weekWall ? Math.round((week / weekWall) * 100) : 0) + "%</b><span>focus this week</span></div>" +
      "<div class=stat><b>" + streak + "d</b><span>streak</span></div>" +
      "<div class=stat><b>" + (all / 3600).toFixed(1) + "h</b><span>all time</span></div>" +
      '<div class=stat title="Share of credited time by trust tier: high, medium, low"><b>' + (tierTotal ? Math.round((tiers.high / tierTotal) * 100) : 0) + "%</b><span>high trust (" +
      (tierTotal ? Math.round((tiers.medium / tierTotal) * 100) : 0) + "% medium)</span></div>";

    var cols = [], max = 1;
    for (var i = 13; i >= 0; i--) {
      var d = new Date(now); d.setDate(d.getDate() - i);
      var v = days[dayKey(d)] || { you: 0, agent: 0 };
      max = Math.max(max, v.you, v.agent);
      cols.push({ d: d, v: v, today: i === 0 });
    }
    document.getElementById("daily").innerHTML = cols.map(function (c) {
      return '<div class="day' + (c.today ? " today" : "") + '" title="' + esc(c.d.toDateString()) + ": " + dur(c.v.you) + " credited, agent ran " + dur(c.v.agent) + '">' +
        '<div class="val">' + (c.v.you ? dur(c.v.you) : "") + "</div>" +
        '<div class="bars"><div class="agent" style="height:' + (c.v.agent / max) * 100 + '%"></div><div class="you" style="height:' + (c.v.you / max) * 100 + '%"></div></div>' +
        '<div class="lbl">' + c.d.toLocaleDateString([], { weekday: "short" }).slice(0, 2) + " " + c.d.getDate() + "</div></div>";
    }).join("");

    var pmax = Math.max.apply(null, p.projects.map(function (x) { return x.credited; }).concat([1]));
    document.getElementById("projects").innerHTML = p.projects.map(function (x) {
      return '<div class="bar-row"><div><a href="/u/' + esc(p.user.username) + "/p/" + encodeURIComponent(x.name) + '"><b>' + esc(x.name) + "</b></a> " +
        '<span class="meta">' + x.sessions + " sessions" + (x.commits ? ", " + x.commits + " commits" : "") + (x.last_active ? ", " + ago(x.last_active) : "") + "</span></div>" +
        '<div class="strong num">' + dur(x.credited) + '</div><div class="track"><div class="fill" style="width:' + (x.credited / pmax) * 100 + '%"></div></div></div>';
    }).join("") || '<p class="muted">No projects yet.</p>';

    var hs = Object.keys(harness).sort(function (a, b) { return harness[b] - harness[a]; });
    var hmax = Math.max.apply(null, hs.map(function (h) { return harness[h]; }).concat([1]));
    document.getElementById("harnesses").innerHTML = hs.map(function (h) {
      return '<div class="bar-row"><div>' + esc(h) + '</div><div class="num">' + dur(harness[h]) + '</div><div class="track"><div class="fill" style="width:' + (harness[h] / hmax) * 100 + '%"></div></div></div>';
    }).join("") || '<p class="muted">Nothing this week.</p>';

    document.getElementById("subs").innerHTML = p.submissions.map(function (s) {
      return '<div class="bar-row"><div>' + esc(s.project) + ' <span class="status ' + esc(s.status) + '">' + esc(s.status) + "</span></div>" +
        '<div class="num">' + (s.approved != null ? dur(s.approved) + " of " : "") + dur(s.credited) + "</div></div>";
    }).join("") || '<p class="muted">No hours submitted yet.</p>';

    document.getElementById("sessions").innerHTML = p.sessions.slice(0, 25).map(function (s) {
      var d = new Date(s.start * 1000);
      return "<tr><td><a href=\"/u/" + esc(p.user.username) + "/s/" + encodeURIComponent(s.id) + '">' + esc(d.toLocaleString([], { weekday: "short", hour: "2-digit", minute: "2-digit" })) + "</a></td>" +
        "<td>" + esc(s.project) + '</td><td class="muted">' + esc(s.harness) + '</td><td class="num">' + s.prompts + '</td><td class="num muted">' + dur(s.wall) + '</td><td class="num strong">' + dur(s.credited) + "</td>" +
        '<td><span class="tier ' + esc(s.tier) + '">' + esc(s.tier) + "</span></td><td>" +
        (s.flags || []).map(function (f) { return '<span class="flag">' + esc(f) + "</span>"; }).join("") + "</td></tr>";
    }).join("");
  }

  // ---------- live ----------
  function listen(user, onEvent) {
    if (!window.EventSource) return;
    var es = new EventSource("/live" + (user ? "?u=" + encodeURIComponent(user) : ""));
    es.onmessage = function (m) { try { onEvent(JSON.parse(m.data)); } catch (e) { /* ignore */ } };
  }

  function initHome() {
    var feed = document.getElementById("feed"), first = true;
    listen("", function (e) {
      if (first) { feed.innerHTML = ""; first = false; }
      var li = document.createElement("li");
      li.className = "new";
      li.innerHTML = e.kind === "submission"
        ? '<a href="/u/' + esc(e.user) + '"><b>' + esc(e.user) + "</b></a> submitted hours for <b>" + esc(e.project) + "</b>"
        : '<a href="/u/' + esc(e.user) + '"><b>' + esc(e.user) + "</b></a> synced <b>" + esc(e.project) + '</b> <span class="muted">' + esc(e.harness || "") + ", " + dur(e.credited) + " credited</span>";
      feed.insertBefore(li, feed.firstChild);
      while (feed.children.length > 12) feed.removeChild(feed.lastChild);
      fetch("/api/v1/leaderboard").then(function (r) { return r.json(); }).then(function (d) {
        var rows = d.rows.map(function (r, i) {
          var p = r.wall ? Math.round((r.credited / r.wall) * 100) : 0;
          return '<tr><td class="muted">' + (i + 1) + '</td><td><a href="/u/' + esc(r.username) + '">' + esc(r.username) + '</a></td><td class="num strong">' + dur(r.credited) +
            '</td><td class="num muted">' + dur(r.wall) + '</td><td class="num"><span class="focus" style="--p:' + p + '">' + p + '%</span></td><td class="muted">' + esc(r.top) + "</td></tr>";
        }).join("");
        document.querySelector("#board tbody").innerHTML = rows;
        document.querySelector('[data-k="credited"]').textContent = (d.totals.credited / 3600).toFixed(1) + "h";
        document.querySelector('[data-k="wall"]').textContent = (d.totals.wall / 3600).toFixed(1) + "h";
        document.querySelector('[data-k="users"]').textContent = d.totals.users;
      });
    });
  }

  function initProfile() {
    var p = data();
    renderProfile(p);
    listen(p.user.username, function () {
      fetch("/api/v1/users/" + encodeURIComponent(p.user.username)).then(function (r) { return r.json(); }).then(function (np) {
        renderProfile(np);
        var k = document.querySelector("#kpis .stat");
        if (k) { k.classList.remove("flash"); void k.offsetWidth; k.style.animation = "flash 1.6s ease-out"; }
      });
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    var page = document.body.dataset.page;
    if (page === "home") initHome();
    if (page === "profile") initProfile();
    if (page === "session") document.querySelectorAll("[data-session]").forEach(function (h) { timeline(h, data()); });
    if (page === "review") {
      var ss = data();
      document.querySelectorAll(".timeline[data-idx]").forEach(function (h) { timeline(h, ss[+h.dataset.idx]); });
    }
    document.querySelectorAll("[data-ts]").forEach(function (a) {
      a.textContent = new Date(+a.dataset.ts * 1000).toLocaleString([], { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
    });
  });
})();
