// Pitstop dashboard. Plain JS, no libraries, no external requests.
// Every piece of text is set with textContent, never innerHTML.
"use strict";

const API = { headers: { "X-Pitstop": "1" } };
const state = { ov: null, route: "", detail: null, history: null, filters: { repo: "", agent: "" } };

// ---- small helpers -------------------------------------------------------

// Money is integer micro-dollars; rounding to cents uses integer math.
function usd(m) {
  const neg = m < 0; m = Math.abs(m || 0);
  const c = Math.floor((m + 5000) / 10000);
  return (neg ? "-" : "") + "$" + Math.floor(c / 100) + "." + String(c % 100).padStart(2, "0");
}
function h(tag, attrs, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === "class") e.className = v;
    else if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
    else e.setAttribute(k, v === true ? "" : v);
  }
  for (const k of kids.flat()) if (k != null && k !== false) e.append(k instanceof Node ? k : String(k));
  return e;
}
const num = (t) => h("span", { class: "num" }, t);
function ago(ts) {
  if (!ts) return "–";
  const s = Math.max(0, Math.round(Date.now() / 1000 - ts));
  if (s < 60) return s + "s ago";
  if (s < 3600) return Math.round(s / 60) + " min ago";
  if (s < 86400) return Math.round(s / 3600) + " h ago";
  return new Date(ts * 1000).toLocaleDateString();
}
const clock = (ts) => new Date(ts * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
function toast(msg) {
  const t = document.getElementById("toast");
  t.textContent = msg; t.hidden = false;
  clearTimeout(toast.t); toast.t = setTimeout(() => (t.hidden = true), 4000);
}
async function get(path) {
  const r = await fetch(path, API);
  if (!r.ok) throw new Error(r.status + "");
  return r.json();
}

function chip(status) {
  const cls = { "needs you": "needs", looping: "looping", stopped: "stopped", "on track": "ontrack", tight: "tight", "won't fit": "wontfit" }[status] || "";
  return h("span", { class: "chip " + cls }, status);
}

// Steps bar and money bar side by side; the money bar shows the forecast range
// as a lighter band past what is spent, and a mark at the budget.
function stepsBar(v) {
  if (!v.Total) return h("span", { class: "muted small" }, "no plan");
  const b = h("div", { class: "bar steps", role: "img", "aria-label": v.Done + " of " + v.Total + " steps" }, h("i"));
  b.firstChild.style.width = Math.min(100, (100 * v.Done) / v.Total) + "%";
  return h("div", {}, num(v.Done + "/" + v.Total), b);
}
function moneyBar(v) {
  const top = Math.max(v.Budget || 0, v.Spent + (v.P90 || 0), 1);
  const pct = (x) => Math.min(100, (100 * x) / top) + "%";
  const b = h("div", { class: "bar", role: "img", "aria-label": usd(v.Spent) + " spent" + (v.Budget ? " of " + usd(v.Budget) : "") });
  if (v.Method) {
    const band = h("i", { class: "band" });
    band.style.left = pct(v.Spent + v.P10); band.style.width = "calc(" + pct(v.P90 - v.P10) + ")";
    b.append(band);
  }
  const spent = h("i"); spent.style.width = pct(v.Spent); b.append(spent);
  if (v.Budget) { const cap = h("span", { class: "cap" }); cap.style.left = "calc(" + pct(v.Budget) + " - 1px)"; b.append(cap); }
  return h("div", {}, num(usd(v.Spent) + (v.Budget ? " / " + usd(v.Budget) : "")), b);
}
function rangeText(v) {
  if (!v.Method) return h("span", { class: "muted" }, "–");
  return h("div", {}, num(usd(v.P10) + "–" + usd(v.P90)),
    h("span", { class: "sub" }, v.Confidence + " confidence" + (v.Method === "rate" ? " · from spending rate" : "")));
}

// ---- the decision card ---------------------------------------------------

function confidenceWhy(c) {
  if (c.Reason === "cap") return "The run reached its budget. Nothing more is spent without your answer.";
  if (c.Method === "rate") return "No plan seen, so the range comes from the spending rate. Low confidence.";
  if (c.Confidence === "low") return "Low confidence: only " + c.Done + " step" + (c.Done === 1 ? "" : "s") + " done.";
  return "Medium confidence: " + c.Done + " steps done so far.";
}
function decisionCard(c, timeoutSec) {
  const input = h("input", { type: "text", inputmode: "decimal", value: (c.Offer / 1e6).toFixed(2), "aria-label": "Amount to add, in dollars" });
  const go = h("button", { class: "go", type: "button" }, "Continue");
  const stop = h("button", { type: "button" }, "Stop & save");
  const send = async (cont) => {
    let add = 0;
    if (cont) {
      add = parseUSD(input.value);
      if (!add) { toast("Enter an amount in dollars, e.g. 5 or 2.50"); input.focus(); return; }
    }
    go.disabled = stop.disabled = true;
    const r = await fetch("/pitstop/answer", { method: "POST", headers: { "X-Pitstop": "1", "Content-Type": "application/json" },
      body: JSON.stringify({ id: c.ID, continue: cont, add_micro: add, channel: "dashboard" }) });
    if (r.status === 409) toast("Already answered elsewhere, or the time ran out.");
    else if (!r.ok) { toast("Could not send the answer (" + r.status + ")."); go.disabled = stop.disabled = false; }
    else toast(cont ? "Continued +" + usd(add) + "." : "Stopped. Work saved. Resume with: pitstop resume " + c.Run);
  };
  go.addEventListener("click", () => send(true));
  stop.addEventListener("click", () => send(false));
  input.addEventListener("keydown", (e) => { if (e.key === "Enter") send(true); });
  const left = timeoutSec ? c.Asked && Math.max(0, Date.parse(c.Asked) / 1000 + timeoutSec - Date.now() / 1000) : 0;
  const cd = h("span", { class: "countdown", "data-deadline": timeoutSec ? String(Date.parse(c.Asked) / 1000 + timeoutSec) : "" }, left ? countdownText(left) : "");
  const facts = [];
  if (c.Total) facts.push(["Steps", c.Done + " of " + c.Total]);
  facts.push(["Spent", usd(c.Spent) + " of " + usd(c.Budget)]);
  if (c.Reason !== "cap") facts.push(["Likely more to finish", usd(c.P10) + "–" + usd(c.P90)]);
  return h("section", { class: "card", "aria-label": "Decision needed for run " + c.Run },
    h("div", { class: "kicker" }, "Needs your decision"),
    h("h3", {}, c.Reason === "cap" ? "Budget reached on run " + c.Run + "." : "Your agent's paused. Here's what finishing costs."),
    h("div", { class: "facts" }, facts.map(([k, v]) => h("div", {}, h("span", {}, k), h("b", {}, v)))),
    h("p", { class: "why" }, confidenceWhy(c),
      (c.LastActions || []).length ? " Last actions: " + c.LastActions.join(", ") + "." : ""),
    h("div", { class: "actions" },
      h("span", { class: "amount" }, "+$", input), go, stop, cd,
      h("a", { href: "#/run/" + encodeURIComponent(c.Run), class: "small" }, "Run details")));
}
function countdownText(s) {
  s = Math.round(s);
  return "Stops by itself in " + Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0") + " with no answer. Never continues on its own.";
}
// Exact dollars to micro-dollars, digit by digit (no float rounding).
function parseUSD(s) {
  s = String(s).trim().replace(/^\$/, "");
  const m = /^(\d*)(?:\.(\d{0,6}))?$/.exec(s);
  if (!m || (m[1] === "" && !m[2])) return 0;
  return Number(m[1] || "0") * 1e6 + Number((m[2] || "").padEnd(6, "0"));
}
setInterval(() => {
  for (const e of document.querySelectorAll("[data-deadline]")) {
    const d = Number(e.dataset.deadline);
    if (d) e.textContent = countdownText(Math.max(0, d - Date.now() / 1000));
  }
}, 1000);

// ---- screens -------------------------------------------------------------

function renderLive(view) {
  const o = state.ov;
  if (!o) { view.replaceChildren(h("p", { class: "muted" }, "Loading…")); return; }
  const stat = (label, value) => h("div", { class: "stat" }, h("b", {}, value), h("span", {}, label));
  const acc = o.AccScored >= o.AccMin
    ? stat("forecast range right (" + o.AccUnder + " too high, " + o.AccOver + " too low)", o.AccHits + " of " + o.AccScored)
    : stat("forecast accuracy appears after " + o.AccMin + " finished runs", o.AccScored + " of " + o.AccMin);
  const kids = [
    h("h1", { class: "sr" }, "Live"),
    h("div", { class: "strip" },
      stat("spent today", usd(o.Day) + (o.DayCap ? " / " + usd(o.DayCap) : "")),
      stat("spent this month", usd(o.Month) + (o.MonthCap ? " / " + usd(o.MonthCap) : "")),
      acc),
  ];
  for (const c of o.Cards || []) kids.push(decisionCard(c, o.TimeoutSec));
  for (const d of o.Recent || []) {
    if (!d.Answer) continue;
    const what = d.Answer === "continue" ? "continued +" + usd(d.Added) : d.Answer === "timeout" ? "stopped after no answer. Work saved" : "stopped. Work saved";
    kids.push(h("p", { class: "answered" }, "Run " + d.Run + ": " + what + (d.Channel && d.Answer !== "timeout" ? " (answered in " + d.Channel + ")" : "") + "."));
  }
  if (!(o.Runs || []).length) {
    kids.push(h("div", { class: "empty" }, h("p", {}, "No runs yet. Start the proxy, then run your agent through it:"),
      h("pre", {}, "pitstop serve\npitstop run -- claude")));
    view.replaceChildren(...kids);
    return;
  }
  const rows = o.Runs.map((v) => {
    const tr = h("tr", { class: "link", tabindex: "0" },
      h("td", {}, h("a", { href: "#/run/" + encodeURIComponent(v.ID) }, h("span", { class: "num" }, v.ID)), h("span", { class: "sub" }, v.Agent || "")),
      h("td", { class: "hide-sm" }, v.Repo || "–", v.Branch ? h("span", { class: "sub" }, v.Branch) : null),
      h("td", { class: "hide-sm" }, stepsBar(v)),
      h("td", {}, moneyBar(v)),
      h("td", { class: "hide-sm" }, rangeText(v)),
      h("td", {}, chip(v.Status), v.Fit && v.Status !== "needs you" ? h("span", { class: "sub" }, v.Fit) : null));
    const open = () => (location.hash = "#/run/" + encodeURIComponent(v.ID));
    tr.addEventListener("click", (e) => { if (e.target.tagName !== "A") open(); });
    tr.addEventListener("keydown", (e) => { if (e.key === "Enter") open(); });
    return tr;
  });
  kids.push(h("h2", {}, "Runs"), h("table", {},
    h("thead", {}, h("tr", {}, h("th", {}, "Run"), h("th", { class: "hide-sm" }, "Repo"), h("th", { class: "hide-sm" }, "Steps"), h("th", {}, "Money"),
      h("th", { class: "hide-sm" }, "Likely more"), h("th", {}, "Status"))),
    h("tbody", {}, rows)));
  view.replaceChildren(...kids);
}

// Cost curve: spend over time (step line), budget line, and the forecast range
// as a fan from the latest point to a projected finish.
function curve(d) {
  const pts = d.Series || [];
  const W = 760, H = 260, L = 64, R = 16, T = 12, B = 28;
  if (pts.length === 0) return h("p", { class: "muted" }, "No calls yet.");
  const t0 = pts[0].At, tLast = pts[pts.length - 1].At;
  const span = Math.max(tLast - t0, 60);
  const tEnd = d.Method ? tLast + Math.max(span * 0.3, 60) : tLast + span * 0.05;
  const spent = pts[pts.length - 1].Cum;
  const yMax = Math.max(d.Budget || 0, spent + (d.P90 || 0), spent, 1) * 1.08;
  const x = (t) => L + ((t - t0) / Math.max(tEnd - t0, 1)) * (W - L - R);
  const y = (m) => T + (1 - m / yMax) * (H - T - B);
  const NS = "http://www.w3.org/2000/svg";
  const s = (tag, attrs, text) => { const e = document.createElementNS(NS, tag); for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v); if (text != null) e.textContent = text; return e; };
  const summary = "Spent " + usd(spent) + (d.Budget ? " of " + usd(d.Budget) : "") + (d.Method ? "; likely " + usd(d.P10) + " to " + usd(d.P90) + " more" : "");
  const svg = s("svg", { viewBox: "0 0 " + W + " " + H, class: "curve", role: "img", "aria-label": "Cost over time. " + summary });
  for (let i = 0; i <= 4; i++) {
    const m = (yMax * i) / 4, yy = y(m);
    svg.append(s("line", { x1: L, x2: W - R, y1: yy, y2: yy, stroke: "rgba(91,100,114,.15)" }));
    svg.append(s("text", { x: L - 8, y: yy + 4, "text-anchor": "end" }, usd(m)));
  }
  svg.append(s("text", { x: L, y: H - 8 }, clock(t0)));
  svg.append(s("text", { x: x(tLast), y: H - 8, "text-anchor": "middle" }, clock(tLast)));
  if (d.Method) {
    const x1 = x(tLast), x2 = x(tEnd);
    svg.append(s("polygon", { points: [x1 + "," + y(spent), x2 + "," + y(spent + d.P90), x2 + "," + y(spent + d.P10)].join(" "), fill: "rgba(91,100,114,.22)" }));
    svg.append(s("line", { x1, y1: y(spent), x2, y2: y(spent + d.P50), stroke: "#5B6472", "stroke-dasharray": "4 4" }));
    svg.append(s("text", { x: x2 - 4, y: y(spent + d.P90) - 6, "text-anchor": "end" }, "likely " + usd(spent + d.P10) + "–" + usd(spent + d.P90)));
  }
  if (d.Budget) {
    svg.append(s("line", { x1: L, x2: W - R, y1: y(d.Budget), y2: y(d.Budget), stroke: "#14161A", "stroke-dasharray": "6 4" }));
    svg.append(s("text", { x: L + 6, y: y(d.Budget) - 6 }, "budget " + usd(d.Budget)));
  }
  let path = "M" + x(t0) + "," + y(0);
  for (const p of pts) path += " H" + x(p.At) + " V" + y(p.Cum);
  svg.append(s("path", { d: path, fill: "none", stroke: "#14161A", "stroke-width": "2" }));
  svg.append(s("circle", { cx: x(tLast), cy: y(spent), r: 4, fill: "#14161A" }));
  return h("div", {}, svg, h("div", { class: "legend" },
    h("span", {}, h("i"), "spent"), d.Method ? h("span", {}, h("i", { class: "band" }), "likely range to finish") : null,
    d.Budget ? h("span", {}, h("i", { class: "cap" }), "budget") : null));
}

function renderRun(view, id) {
  const d = state.detail;
  if (!d || d.ID !== id) { view.replaceChildren(h("p", { class: "muted" }, "Loading run " + id + "…")); return; }
  const copy = h("button", { type: "button" }, "Copy");
  copy.addEventListener("click", async () => {
    try { await navigator.clipboard.writeText(d.Resume); toast("Copied: " + d.Resume); } catch { toast("Copy failed: select the command instead."); }
  });
  const stat = (label, value, sub) => h("div", { class: "stat" }, h("b", {}, value), h("span", {}, label), sub ? h("span", { class: "sub" }, sub) : null);
  const kids = [
    h("a", { class: "back", href: "#/" }, "← Live"),
    h("h1", { class: "num" }, d.ID),
    h("p", { class: "meta" }, [d.Agent || "agent unknown", d.Repo ? d.Repo + (d.Branch ? " @ " + d.Branch : "") : null,
      d.Started ? "started " + ago(d.Started) : null, d.LastCall ? "last call " + ago(d.LastCall) : null].filter(Boolean).join(" · "), " ", chip(d.Status)),
  ];
  if (d.Card) kids.push(decisionCard(d.Card, state.ov ? state.ov.TimeoutSec : 0));
  kids.push(h("div", { class: "strip" },
    stat("spent" + (d.Budget ? " of budget" : ""), usd(d.Spent) + (d.Budget ? " / " + usd(d.Budget) : "")),
    stat("likely more to finish", d.Method ? usd(d.P10) + "–" + usd(d.P90) : "–",
      d.Method ? d.Confidence + " confidence" + (d.Method === "rate" ? ", from spending rate" : "") + (d.Fit ? " · " + d.Fit : "") : "not enough to forecast yet"),
    stat("steps", d.Total ? d.Done + " / " + d.Total : "no plan"),
    stat("calls", String(d.Calls))));
  const side = [h("h3", {}, "Pick up where you left off"), h("div", { class: "cmd" }, h("code", {}, d.Resume), copy)];
  if ((d.Checkpoints || []).length) {
    side.push(h("h3", { class: "mt" }, "Saved work"), h("ul", { class: "plain" },
      d.Checkpoints.slice().reverse().map((c) => h("li", {}, h("code", {}, c.Branch), h("span", { class: "sub" }, c.Reason + " · " + ago(c.At) + " · " + c.Commit.slice(0, 8))))));
  }
  if ((d.Decisions || []).length) {
    side.push(h("h3", { class: "mt" }, "Decisions"), h("ul", { class: "plain" }, d.Decisions.map((x) =>
      h("li", {}, (x.Reason === "cap" ? "Budget reached" : "Forecast") + ": " + (x.Answer === "continue" ? "continued +" + usd(x.Added) : x.Answer === "timeout" ? "no answer, stopped" : x.Answer === "stop" ? "stopped" : "waiting"),
        h("span", { class: "sub" }, (x.Channel ? x.Channel + " · " : "") + ago(x.Asked))))));
  }
  if ((d.Loops || []).length) {
    side.push(h("h3", { class: "mt" }, "Loop alerts"), h("ul", { class: "plain" }, d.Loops.map((x) =>
      h("li", {}, x.Tool + " " + (x.Kind === "error" ? "same error" : "same call") + " ×" + x.Repeats + " (" + x.Action + ")", h("span", { class: "sub" }, ago(x.At))))));
  }
  kids.push(h("div", { class: "grid2" },
    h("div", { class: "panel" }, h("h3", {}, "Cost over time"), curve(d)),
    h("div", { class: "panel" }, side)));
  if ((d.Plan || []).length) {
    kids.push(h("h2", {}, "Plan"), h("p", { class: "muted small" }, "Cost is what was spent while each step was in progress."),
      h("ol", { class: "plan" }, d.Plan.map((p) => h("li", { class: p.Status },
        h("span", { class: "mark", "aria-label": p.Status }, p.Status === "completed" ? "✓" : p.Status === "in_progress" ? "▸" : "○"),
        h("span", {}, p.Text), num(p.Calls ? usd(p.CostMicro) + " · " + p.Calls + (p.Calls === 1 ? " call" : " calls") : "–")))));
  }
  if ((d.CallList || []).length) {
    kids.push(h("h2", {}, "Calls"), h("table", {},
      h("thead", {}, h("tr", {}, h("th", {}, "Time"), h("th", {}, "Model"), h("th", { class: "r" }, "In"), h("th", { class: "r" }, "Out"),
        h("th", { class: "r hide-sm" }, "Cache read"), h("th", { class: "r hide-sm" }, "Cache write"), h("th", { class: "r" }, "Cost"))),
      h("tbody", {}, d.CallList.map((c) => h("tr", {},
        h("td", {}, num(clock(c.At))), h("td", {}, c.Model),
        h("td", { class: "r" }, num(c.Input.toLocaleString())), h("td", { class: "r" }, num(c.Output.toLocaleString())),
        h("td", { class: "r hide-sm" }, num(c.CacheRead.toLocaleString())), h("td", { class: "r hide-sm" }, num((c.CacheWrite + c.CacheWrite1h).toLocaleString())),
        h("td", { class: "r" }, num(usd(c.CostMicro)), c.Estimated ? h("span", { class: "sub", title: "The stream was cut off; output was estimated on the high side." }, "estimated") : null))))));
  }
  view.replaceChildren(...kids);
}

function renderHistory(view) {
  const d = state.history;
  if (!d) { view.replaceChildren(h("p", { class: "muted" }, "Loading history…")); return; }
  const sel = (label, key, opts) => {
    const s = h("select", { "aria-label": label }, h("option", { value: "" }, "All"), opts.map((o) => h("option", { value: o }, o)));
    s.value = state.filters[key];
    s.addEventListener("change", () => { state.filters[key] = s.value; loadHistory(); });
    return h("label", {}, label, s);
  };
  const headline = d.AccScored >= d.AccMin
    ? "Our range was right " + d.AccHits + " of " + d.AccScored + " times on your runs."
    : "Accuracy appears after " + d.AccMin + " finished runs with a forecast (" + d.AccScored + " so far).";
  const kids = [
    h("h1", {}, "History"),
    h("p", { class: "meta" }, headline, d.AccScored >= d.AccMin ? " Misses: " + d.AccUnder + " too high, " + d.AccOver + " too low. Every miss is shown below." : ""),
    h("div", { class: "filters" }, sel("Repo", "repo", d.Repos || []), sel("Agent", "agent", d.Agents || [])),
  ];
  if (!(d.Runs || []).length) {
    kids.push(h("div", { class: "empty" }, "No runs match."));
  } else {
    kids.push(h("table", {},
      h("thead", {}, h("tr", {}, h("th", {}, "Started"), h("th", {}, "Run"), h("th", { class: "hide-sm" }, "Repo"), h("th", { class: "hide-sm" }, "Agent"),
        h("th", { class: "r" }, "Cost"), h("th", { class: "r hide-sm" }, "Budget"), h("th", {}, "Ended"), h("th", {}, "Forecast"))),
      h("tbody", {}, d.Runs.map((r) => {
        const tr = h("tr", { class: "link", tabindex: "0" },
          h("td", {}, num(r.Started ? new Date(r.Started * 1000).toLocaleString([], { dateStyle: "short", timeStyle: "short" }) : "–")),
          h("td", {}, h("a", { href: "#/run/" + encodeURIComponent(r.ID) }, num(r.ID))),
          h("td", { class: "hide-sm" }, r.Repo || "–", r.Branch ? h("span", { class: "sub" }, r.Branch) : null),
          h("td", { class: "hide-sm" }, r.Agent || "–"),
          h("td", { class: "r" }, num(usd(r.CostMicro))),
          h("td", { class: "r hide-sm" }, num(r.Budget ? usd(r.Budget) : "–")),
          h("td", {}, r.Stopped ? "stopped" : r.Outcome || "open"),
          h("td", {}, r.Verdict ? h("span", {}, r.Verdict === "right" ? "right" : "wrong: " + r.Verdict,
            h("span", { class: "sub num" }, usd(r.Rem) + " vs " + usd(r.P10) + "–" + usd(r.P90))) : h("span", { class: "muted" }, "–")));
        tr.addEventListener("click", (e) => { if (e.target.tagName !== "A") location.hash = "#/run/" + encodeURIComponent(r.ID); });
        tr.addEventListener("keydown", (e) => { if (e.key === "Enter") location.hash = "#/run/" + encodeURIComponent(r.ID); });
        return tr;
      }))));
  }
  view.replaceChildren(...kids);
}

// ---- data and routing ----------------------------------------------------

async function loadDetail(id) {
  try { state.detail = await get("/pitstop/run?id=" + encodeURIComponent(id)); }
  catch { state.detail = { ID: id, missing: true }; }
  render();
}
async function loadHistory() {
  const q = new URLSearchParams(state.filters);
  try { state.history = await get("/pitstop/history?" + q); } catch { /* keep the last one */ }
  render();
}

function route() {
  const hash = location.hash || "#/";
  state.route = hash;
  for (const a of document.querySelectorAll("nav a")) {
    const live = a.dataset.nav === "live" && (hash === "#/" || hash.startsWith("#/run/"));
    const hist = a.dataset.nav === "history" && hash.startsWith("#/history");
    if (live || hist) a.setAttribute("aria-current", "page"); else a.removeAttribute("aria-current");
  }
  if (hash.startsWith("#/run/")) loadDetail(decodeURIComponent(hash.slice(6)));
  else if (hash.startsWith("#/history")) loadHistory();
  render();
  document.getElementById("view").focus({ preventScroll: true });
}

function render() {
  const view = document.getElementById("view");
  // Don't redraw under someone typing an amount into a decision card.
  if (document.activeElement && document.activeElement.closest && document.activeElement.closest(".card input")) return;
  const r = state.route;
  if (r.startsWith("#/run/")) {
    const id = decodeURIComponent(r.slice(6));
    if (state.detail && state.detail.missing && state.detail.ID === id) view.replaceChildren(h("a", { class: "back", href: "#/" }, "← Live"), h("p", {}, "No run named " + id + "."));
    else renderRun(view, id);
    document.title = "Pitstop · " + id;
  } else if (r.startsWith("#/history")) { renderHistory(view); document.title = "Pitstop · History"; }
  else { renderLive(view); document.title = (state.ov && (state.ov.Cards || []).length ? "(" + state.ov.Cards.length + ") Needs you · " : "") + "Pitstop"; }
}

// Live updates: Server-Sent Events, with a reconnecting status in the header.
function connect() {
  const conn = document.getElementById("conn");
  const es = new EventSource("/pitstop/events");
  es.onopen = () => { conn.textContent = "live"; conn.className = "conn live"; };
  es.onerror = () => { conn.textContent = "reconnecting…"; conn.className = "conn"; };
  es.onmessage = (e) => {
    state.ov = JSON.parse(e.data);
    if (state.route.startsWith("#/run/")) loadDetail(decodeURIComponent(state.route.slice(6)));
    else if (state.route.startsWith("#/history")) loadHistory();
    else render();
  };
}

window.addEventListener("hashchange", route);
route();
connect();
