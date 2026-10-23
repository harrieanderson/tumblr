package main

const guiHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>Zoey</title>
<style>
  :root {
    --bg: #07060c;
    --bg2: #0d0b14;
    --panel: rgba(18, 16, 28, .72);
    --panel2: rgba(255,255,255,.03);
    --ink: #f6f3ee;
    --muted: #9d97ab;
    --line: rgba(255,255,255,.08);
    --line2: rgba(232, 213, 181, .18);
    --accent: #c4b5fd;
    --accent2: #a78bfa;
    --deep: #4c1d95;
    --gold: #e8d5b5;
    --ok: #5eead4;
    --warn: #f0abfc;
    --danger: #fb7185;
    --shadow: 0 30px 80px rgba(0,0,0,.45);
    --font: "Segoe UI Variable Display", "Segoe UI Variable", "Segoe UI", system-ui, sans-serif;
    --mono: "Cascadia Code", "Segoe UI Mono", Consolas, monospace;
  }
  * { box-sizing: border-box; }
  html, body { height: 100%; }
  body {
    margin: 0;
    color: var(--ink);
    font: 13.5px/1.45 var(--font);
    background: var(--bg);
    overflow: auto;
  }
  body::before {
    content: "";
    position: fixed;
    inset: 0;
    pointer-events: none;
    background:
      radial-gradient(1000px 520px at -10% -20%, rgba(124, 58, 237, .38), transparent 58%),
      radial-gradient(800px 480px at 110% 0%, rgba(232, 213, 181, .12), transparent 50%),
      radial-gradient(700px 500px at 80% 110%, rgba(168, 85, 247, .16), transparent 55%),
      linear-gradient(180deg, #0c0a12 0%, var(--bg) 40%, #05040a 100%);
    z-index: -2;
  }
  .noise {
    position: fixed;
    inset: 0;
    pointer-events: none;
    z-index: -1;
    opacity: .09;
    background-image: url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='140' height='140'><filter id='n'><feTurbulence type='fractalNoise' baseFrequency='.85' numOctaves='2' stitchTiles='stitch'/></filter><rect width='100%' height='100%' filter='url(%23n)' opacity='.55'/></svg>");
  }
  .app {
    max-width: 1280px;
    margin: 0 auto;
    padding: 22px 22px 28px;
  }
  .topbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    margin-bottom: 18px;
  }
  .brand {
    display: flex;
    align-items: center;
    gap: 12px;
  }
  .mark {
    width: 42px;
    height: 42px;
    border-radius: 14px;
    display: grid;
    place-items: center;
    font-weight: 700;
    letter-spacing: .04em;
    color: #1a1024;
    background:
      linear-gradient(160deg, #f3e7d3 0%, #e8d5b5 35%, #c4b5fd 100%);
    box-shadow: 0 10px 30px rgba(196, 181, 253, .28), inset 0 1px 0 rgba(255,255,255,.7);
  }
  .brand-kicker {
    font-size: 10px;
    letter-spacing: .28em;
    text-transform: uppercase;
    color: var(--gold);
  }
  h1 {
    margin: 1px 0 0;
    font-size: 1.7rem;
    font-weight: 620;
    letter-spacing: -.04em;
  }
  .sub {
    margin: 2px 0 0;
    color: var(--muted);
    font-size: 12.5px;
  }
  .status {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 12px 8px 10px;
    border-radius: 999px;
    border: 1px solid var(--line);
    background: rgba(255,255,255,.03);
    backdrop-filter: blur(16px);
    color: var(--muted);
    font-size: 11px;
    letter-spacing: .16em;
    text-transform: uppercase;
  }
  .status i {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: #6b6578;
    box-shadow: 0 0 0 4px rgba(107,101,120,.15);
  }
  .status.on {
    color: var(--ok);
    border-color: rgba(94, 234, 212, .28);
  }
  .status.on i {
    background: var(--ok);
    box-shadow: 0 0 0 4px rgba(94, 234, 212, .16), 0 0 18px var(--ok);
    animation: pulse 1.8s ease-in-out infinite;
  }
  .top-right {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-shrink: 0;
  }
  .tally {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 14px 8px 12px;
    border-radius: 18px;
    border: 1px solid var(--line2);
    background: rgba(255,255,255,.03);
    backdrop-filter: blur(16px);
    min-width: 168px;
  }
  .tally-new {
    font-size: 1.45rem;
    font-weight: 700;
    letter-spacing: -.04em;
    color: var(--ok);
    line-height: 1;
  }
  .tally-new.zero { color: var(--muted); }
  .tally-kicker {
    font-size: 10px;
    letter-spacing: .16em;
    text-transform: uppercase;
    color: var(--gold);
  }
  .tally-total {
    margin-top: 1px;
    color: var(--muted);
    font-size: 12px;
  }
  @keyframes pulse {
    50% { opacity: .45; }
  }
  .layout {
    display: grid;
    grid-template-columns: 340px 1fr;
    gap: 14px;
  }
  @media (max-width: 920px) { .layout { grid-template-columns: 1fr; } }
  .card {
    position: relative;
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 22px;
    padding: 16px 16px 15px;
    box-shadow: var(--shadow);
    backdrop-filter: blur(22px);
    overflow: hidden;
  }
  .card::before {
    content: "";
    position: absolute;
    inset: 0 0 auto;
    height: 1px;
    background: linear-gradient(90deg, transparent, rgba(232,213,181,.45), rgba(196,181,253,.4), transparent);
  }
  .card h2 {
    margin: 0 0 4px;
    font-size: .78rem;
    font-weight: 650;
    letter-spacing: .18em;
    text-transform: uppercase;
    color: var(--gold);
  }
  .hint { margin: 0 0 12px; color: var(--muted); line-height: 1.5; }
  .row {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    align-items: end;
    margin-bottom: 8px;
  }
  .field { display: flex; flex-direction: column; gap: 5px; min-width: 88px; }
  .field.grow { flex: 1; min-width: 140px; }
  .field > label, .lbl { font-size: 10px; letter-spacing: .14em; text-transform: uppercase; color: var(--muted); }
  .acct label.grow {
    text-transform: none;
    letter-spacing: 0;
    font-size: 13px;
    color: var(--ink);
  }
  input[type="text"], input[type="number"], select, textarea {
    width: 100%;
    background: rgba(0,0,0,.28);
    color: var(--ink);
    border: 1px solid var(--line);
    border-radius: 12px;
    padding: 9px 11px;
    font: inherit;
    outline: none;
    transition: border-color .2s, box-shadow .2s;
  }
  input:focus, select:focus, textarea:focus {
    border-color: rgba(196,181,253,.55);
    box-shadow: 0 0 0 4px rgba(139, 92, 246, .16);
  }
  textarea { min-height: 72px; resize: vertical; }
  button {
    appearance: none;
    border: 0;
    border-radius: 12px;
    padding: 9px 14px;
    font: inherit;
    font-weight: 650;
    letter-spacing: .01em;
    cursor: pointer;
    color: #160b22;
    background: linear-gradient(180deg, #f4ead8 0%, #e8d5b5 38%, #c4b5fd 140%);
    box-shadow: 0 10px 24px rgba(196, 181, 253, .18), inset 0 1px 0 rgba(255,255,255,.45);
    transition: transform .15s ease, filter .15s ease, opacity .15s;
  }
  button:hover { transform: translateY(-1px); filter: brightness(1.06); }
  button:active { transform: translateY(0); }
  button.ghost {
    background: rgba(255,255,255,.03);
    color: var(--ink);
    border: 1px solid var(--line);
    box-shadow: none;
  }
  button.ghost:hover { border-color: rgba(232,213,181,.35); background: rgba(255,255,255,.05); }
  button:disabled { opacity: 0.38; cursor: not-allowed; transform: none; filter: none; }
  .acct-tools { display: flex; gap: 6px; flex-wrap: wrap; margin-bottom: 10px; }
  .acct-list {
    height: 348px;
    overflow: auto;
    background: rgba(0,0,0,.22);
    border: 1px solid var(--line);
    border-radius: 16px;
    padding: 6px;
  }
  .acct-list::-webkit-scrollbar { width: 8px; }
  .acct-list::-webkit-scrollbar-thumb { background: rgba(255,255,255,.12); border-radius: 99px; }
  .acct {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 8px;
    border-radius: 14px;
    margin-bottom: 2px;
  }
  .acct:hover { background: rgba(196, 181, 253, .07); }
  .acct .grow { flex: 1; min-width: 0; }
  .acct label.grow {
    display: flex;
    align-items: center;
    gap: 10px;
    cursor: pointer;
    color: var(--ink);
  }
  .acct input { accent-color: #c4b5fd; }
  .avatar {
    flex: 0 0 32px;
    width: 32px;
    height: 32px;
    border-radius: 11px;
    display: grid;
    place-items: center;
    font-size: 12px;
    font-weight: 700;
    color: #1b1227;
    background: linear-gradient(160deg, #e8d5b5, #c4b5fd);
  }
  .acct .meta {
    color: var(--muted);
    font-size: 11px;
    display: flex;
    align-items: center;
    gap: 6px;
    margin-top: 1px;
  }
  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: #6b6578;
  }
  .dot.live { background: var(--ok); box-shadow: 0 0 8px var(--ok); }
  .acct .btn-open-one {
    flex: 0 0 auto;
    padding: 6px 10px;
    font-size: 11px;
    letter-spacing: .08em;
    text-transform: uppercase;
    background: rgba(255,255,255,.03);
    color: var(--gold);
    border: 1px solid var(--line2);
    box-shadow: none;
  }
  .count { margin-top: 10px; color: var(--muted); font-size: 12px; }
  .grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 14px;
  }
  @media (max-width: 720px) { .grid { grid-column: 1 / -1; grid-template-columns: 1fr; } }
  .span-all { grid-column: 1 / -1; }
  .hero {
    display: flex;
    justify-content: space-between;
    gap: 16px;
    align-items: flex-end;
  }
  .hero p { max-width: 62ch; }
  .check {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 10px 0 14px;
    color: var(--muted);
  }
  .log-wrap { margin-top: 14px; }
  .log-head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
  }
  #log {
    height: 220px;
    overflow: auto;
    background: #05040a;
    border: 1px solid var(--line);
    border-radius: 16px;
    padding: 12px 14px;
    font: 12px/1.55 var(--mono);
    white-space: pre-wrap;
  }
  .log-line { color: #c9c3d6; }
  .log-line.go { color: var(--accent); }
  .log-line.ok { color: var(--ok); }
  .log-line.err { color: var(--danger); }
  input[type="file"] {
    width: 100%;
    color: var(--muted);
    background: rgba(0,0,0,.2);
    border: 1px dashed rgba(232,213,181,.25);
    border-radius: 12px;
    padding: 8px;
  }
  input[type="file"]::file-selector-button {
    background: rgba(255,255,255,.04);
    color: var(--gold);
    border: 1px solid var(--line);
    border-radius: 10px;
    padding: 8px 12px;
    margin-right: 10px;
    font: inherit;
    letter-spacing: .08em;
    text-transform: uppercase;
    cursor: pointer;
  }
</style>
</head>
<body>
<div class="noise"></div>
<div class="app">
  <div class="topbar">
    <div class="brand">
      <div class="mark">Z</div>
      <div>
        <div class="brand-kicker">Studio</div>
        <h1>Zoey</h1>
        <p class="sub">Pick accounts, then run setup, reach, posts, or DMs.</p>
      </div>
    </div>
    <div class="top-right">
      <div id="tally" class="tally">
        <div class="tally-new zero" id="tallyNew">+0</div>
        <div>
          <div class="tally-kicker" id="tallyCaption">new today</div>
          <div class="tally-total" id="tallyTotal">0 followers</div>
        </div>
      </div>
      <label class="check" title="Bot Chrome stays hidden, including logins. Captchas can't be solved while this is on."><input id="headless" type="checkbox" /> Hide Chrome</label>
      <div id="status" class="status"><i></i><span id="statusText">Idle</span></div>
    </div>
  </div>

  <div class="layout">
    <section class="card">
      <h2>Accounts</h2>
      <p class="hint">Tick blogs for a job. Watch opens that profile in Chrome.</p>
      <div class="row">
        <div class="field grow">
          <label for="acctFilter">Filter</label>
          <input id="acctFilter" type="text" placeholder="name, email, blog" />
        </div>
      </div>
      <div class="acct-tools">
        <button type="button" id="btnSelectAll" data-keep="1">Select all</button>
        <button type="button" id="btnSelectNone" class="ghost" data-keep="1">None</button>
        </div>
      <div class="acct-tools" role="group" aria-label="Select top accounts by followers">
        <button type="button" id="btnTopMinus10" class="ghost" data-keep="1" aria-label="Decrease top count by 10">−10</button>
        <button type="button" id="btnTopMinus1" class="ghost" data-keep="1" aria-label="Decrease top count by 1">−1</button>
        <button type="button" id="btnSelect20" data-keep="1">Select top 20</button>
        <button type="button" id="btnTopPlus1" class="ghost" data-keep="1" aria-label="Increase top count by 1">+1</button>
        <button type="button" id="btnTopPlus10" class="ghost" data-keep="1" aria-label="Increase top count by 10">+10</button>
      </div>
      <p class="hint">List order: most followers first; same count → Zoey-setup above not-setup. Top N uses that order.</p>
      <div id="acctList" class="acct-list"></div>
      <div id="acctCount" class="count">0 selected</div>
      <div class="acct-tools" style="margin-top:12px;margin-bottom:0">
        <button type="button" id="btnLogin">Login selected</button>
        <button type="button" id="btnOpenChrome" class="ghost" data-keep="1">Open Chrome</button>
      </div>
    </section>

    <div class="grid">
      <section class="card span-all">
        <h2>Account setup</h2>
        <div class="hero">
          <p class="hint">Account settings first: English, birthday 29 Dec 2000, then AgeKey. Appearance (pic/banner/colours/username) is last. Type the AgeKey passcode yourself if Windows Security appears.</p>
          <div class="row" style="margin:0;flex-shrink:0">
            <button type="button" id="btnSetup">Run full setup</button>
            <button type="button" id="btnSetupSkip" class="ghost" data-keep="1">Skip</button>
          </div>
        </div>
      </section>

      <section class="card">
        <h2>Reach</h2>
        <p class="hint">Like and follow likers until the minutes run out.</p>
        <div class="row">
          <div class="field">
            <label for="reachMinutes">Minutes</label>
            <input id="reachMinutes" type="number" min="1" max="720" value="90" />
          </div>
          <div class="field">
            <label for="reachParallel">At a time</label>
            <input id="reachParallel" type="number" min="1" max="40" value="10" />
          </div>
          <div class="field">
            <label for="followCount">Follows each</label>
            <input id="followCount" type="number" min="0" max="80" value="48" />
          </div>
          <div class="field">
            <label for="likePosts">Likes each</label>
            <input id="likePosts" type="number" min="1" max="10" value="4" />
          </div>
        </div>
        <button type="button" id="btnReach">Start reach</button>
        <button type="button" id="btnReachMid" class="ghost">Reach + replies+</button>
        <button type="button" id="btnGrowth" class="ghost">Growth report</button>
        <button type="button" id="btnResearch" class="ghost">Research report</button>
        <a href="/research" target="_blank" class="ghost" style="margin-left:6px">Open research dashboard ↗</a>
        <p class="hint">Research report: which seed blogs, accounts, engage styles and DM replies turn into followbacks and DMs. Runs alongside reach, auto-syncs every 2h, and feeds the reach tuner (also saved to research-report.txt).</p>
        <p class="hint">Replies+: every selected blog runs reach and DM replies for the same minutes. Inboxes are checked in turns, busiest first.</p>
      </section>

      <section class="card">
        <h2>Messages</h2>
        <p class="hint">Sends replies, then keeps checking for new DMs until the watch window ends.</p>
        <label class="check"><input id="msgDry" type="checkbox" /> Dry run (do not send)</label>
        <label class="check"><input id="msgWatch" type="checkbox" checked /> Keep watching for new DMs</label>
        <div class="row">
          <div class="field">
            <label for="msgHours">Watch hours</label>
            <input id="msgHours" type="number" min="1" max="24" value="4" />
          </div>
        </div>
        <button type="button" id="btnMessages">Run replies</button>
      </section>

      <section class="card">
        <h2>Picture post</h2>
        <p class="hint">Post from pics/posted or upload onto the ticked blogs.</p>
        <div class="row">
          <div class="field grow">
            <label for="postedPic">pics/posted</label>
            <select id="postedPic"><option value="">(none)</option></select>
          </div>
        </div>
        <div class="row">
          <div class="field grow">
            <label for="photoFile">Or upload</label>
            <input id="photoFile" type="file" accept="image/*,video/*" />
          </div>
        </div>
        <div class="row">
          <div class="field grow">
            <label for="photoCaption">Caption</label>
            <textarea id="photoCaption" rows="4" placeholder="optional — Enter for a new line"></textarea>
          </div>
        </div>
        <label class="check"><input id="photoPin" type="checkbox" /> Pin this post (replaces the current pin)</label>
        <button type="button" id="btnPhoto">Post photo</button>
      </section>

      <section class="card">
        <h2>More</h2>
        <div class="row">
          <div class="field grow">
            <label for="textBody">Text post</label>
            <textarea id="textBody" placeholder="what's on your mind"></textarea>
          </div>
        </div>
        <button type="button" id="btnText" class="ghost">Post text</button>
        <div class="row" style="margin-top:12px">
          <div class="field grow">
            <label for="reblogCat">Reblog</label>
            <select id="reblogCat">
              <option value="mix">Mix</option>
              <option value="cats">Cats</option>
              <option value="nature">Nature</option>
              <option value="cute">Cute</option>
              <option value="horny">Horny</option>
            </select>
          </div>
          <div class="field">
            <label for="reblogCount">Count</label>
            <input id="reblogCount" type="number" min="1" max="20" value="3" />
          </div>
        </div>
        <button type="button" id="btnReblog" class="ghost">Reblog</button>
        <div class="row" style="margin-top:12px">
          <div class="field">
            <label for="nsfwCount">Like NSFW</label>
            <input id="nsfwCount" type="number" min="1" max="40" value="1" />
          </div>
        </div>
        <button type="button" id="btnNsfw" class="ghost">Like NSFW</button>
      </section>
    </div>
  </div>

  <section class="card log-wrap">
    <div class="log-head">
      <h2>Log</h2>
      <span class="sub">live output</span>
    </div>
    <div id="log"></div>
  </section>
</div>
<script>
const logEl = document.getElementById("log");
const statusEl = document.getElementById("status");
const statusText = document.getElementById("statusText");
const tallyNewEl = document.getElementById("tallyNew");
const tallyCaptionEl = document.getElementById("tallyCaption");
const tallyTotalEl = document.getElementById("tallyTotal");
const buttons = [...document.querySelectorAll("button")];
const acctList = document.getElementById("acctList");
const acctCount = document.getElementById("acctCount");
const acctFilter = document.getElementById("acctFilter");
let accountsCache = [];
let selectedIds = {};
let topAccountCount = 20;

function formatTallyDay(day) {
  const months = ["Jan","Feb","Mar","Apr","May","Jun","Jul","Aug","Sep","Oct","Nov","Dec"];
  const parts = String(day || "").split("-");
  if (parts.length !== 3) return "new today";
  const d = parseInt(parts[2], 10);
  const m = parseInt(parts[1], 10);
  if (!d || !m || m < 1 || m > 12) return "new today";
  return "new today · " + d + " " + months[m - 1];
}

function applyTally(tally) {
  if (!tally || typeof tally !== "object") return;
  const n = Number(tally.todayNew || 0) || 0;
  const total = Number(tally.total || 0) || 0;
  tallyNewEl.textContent = n > 0 ? "+" + n : "+0";
  tallyNewEl.classList.toggle("zero", n <= 0);
  tallyCaptionEl.textContent = formatTallyDay(tally.date);
  tallyTotalEl.textContent = total.toLocaleString() + " followers";
}

function selectedAccountIds() {
  return Object.keys(selectedIds).filter(function (id) { return selectedIds[id]; });
}

function updateCount() {
  const n = selectedAccountIds().length;
  const total = accountsCache.filter(function (a) { return a.enabled; }).length;
  acctCount.textContent = n + " selected  /  " + total + " enabled";
}

function escapeHtml(s) {
  return String(s || "").replace(/[&<>"']/g, function (c) {
    return ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c];
  });
}

function accountsByFollowers() {
  return accountsCache.slice().sort(function (a, b) {
    const df = (b.followers || 0) - (a.followers || 0);
    if (df !== 0) return df;
    const az = a.zoeySetup ? 1 : 0;
    const bz = b.zoeySetup ? 1 : 0;
    if (az !== bz) return bz - az; // Zoey-setup before not-setup
    return String(a.id || "").toLowerCase().localeCompare(String(b.id || "").toLowerCase());
  });
}

function renderAccountList() {
  const q = (acctFilter.value || "").toLowerCase().trim();
  const html = [];
  accountsByFollowers().forEach(function (a) {
    if (!a.enabled) return;
    const hay = ((a.id || "") + " " + (a.email || "") + " " + (a.blog || "")).toLowerCase();
    if (q && hay.indexOf(q) < 0) return;
    const mark = a.loggedIn ? "logged in" : "needs login";
    const title = a.blog ? "@" + a.blog : a.id;
    const extra = a.blog ? " · " + a.id : "";
    const fol = a.followers ? " · " + a.followers + " followers" : "";
    const zoey = a.zoeySetup ? " · Zoey" : "";
    const checked = selectedIds[a.id] ? " checked" : "";
    const initial = escapeHtml(((a.blog || a.id || "?").replace(/^@/, "").charAt(0) || "?").toUpperCase());
    const live = a.loggedIn ? " live" : "";
    html.push(
      '<div class="acct">' +
        '<label class="grow">' +
          '<input type="checkbox" data-id="' + escapeHtml(a.id) + '"' + checked + ' />' +
          '<span class="avatar">' + initial + '</span>' +
          '<span><strong>' + escapeHtml(title) + '</strong><div class="meta"><span class="dot' + live + '"></span>' + mark + escapeHtml(extra + fol + zoey) + '</div></span>' +
        '</label>' +
        '<button type="button" class="btn-open-one" data-id="' + escapeHtml(a.id) + '" data-keep="1">Watch</button>' +
      '</div>'
    );
  });
  acctList.innerHTML = html.join("") || '<div class="acct">No accounts match.</div>';
  acctList.querySelectorAll("input[type=checkbox]").forEach(function (box) {
    box.onchange = function () {
      selectedIds[box.getAttribute("data-id")] = box.checked;
      updateCount();
    };
  });
  acctList.querySelectorAll(".btn-open-one").forEach(function (btn) {
    btn.onclick = function (ev) {
      ev.preventDefault();
      ev.stopPropagation();
      openChromeFor(btn.getAttribute("data-id"));
    };
  });
  updateCount();
}

function selectAll() {
  accountsByFollowers().forEach(function (a) {
    if (a.enabled) selectedIds[a.id] = true;
  });
  renderAccountList();
}

function selectNone() {
  selectedIds = {};
  renderAccountList();
}

function adjustTopCount(delta) {
  topAccountCount = Math.max(1, topAccountCount + delta);
  document.getElementById("btnSelect20").textContent = "Select top " + topAccountCount;
  document.getElementById("btnTopMinus1").disabled = topAccountCount <= 1;
  document.getElementById("btnTopMinus10").disabled = topAccountCount <= 1;
}

function selectTopAccounts() {
  selectedIds = {};
  const seen = {};
  let n = 0;
  accountsByFollowers().forEach(function (a) {
    if (!a.enabled || !a.loggedIn || n >= topAccountCount) return;
    const key = (a.blog || a.id).toLowerCase();
    if (seen[key]) return;
    seen[key] = true;
    selectedIds[a.id] = true;
    n++;
  });
  renderAccountList();
}

async function loadAccounts() {
  try {
    const r = await fetch("/api/accounts");
    const j = await r.json();
    accountsCache = j.accounts || [];
    applyTally(j.tally);
    if (Object.keys(selectedIds).length === 0) {
      const active = accountsCache.find(function (a) { return a.active && a.enabled; });
      if (active) selectedIds[active.id] = true;
    }
    renderAccountList();
  } catch (e) {
    acctList.innerHTML = '<div class="acct">Could not load accounts.</div>';
  }
}

async function loadPostedPics() {
  try {
    const r = await fetch("/api/posted-pics");
    const j = await r.json();
    const sel = document.getElementById("postedPic");
    const keep = sel.value;
    const pics = j.pics || [];
    sel.innerHTML = '<option value="">(pick a file)</option>' +
      pics.map(function (p) { return '<option value="' + p + '">' + p + '</option>'; }).join("");
    if (keep) sel.value = keep;
  } catch (_) {}
}

function appendLog(line) {
  const row = document.createElement("div");
  row.className = "log-line";
  if (/Error:/.test(line)) row.classList.add("err");
  else if (/Done\./.test(line)) row.classList.add("ok");
  else if (/Starting:/.test(line)) row.classList.add("go");
  row.textContent = line;
  logEl.appendChild(row);
  logEl.scrollTop = logEl.scrollHeight;
}

function setBusy(busy) {
  statusText.textContent = busy ? "Running" : "Idle";
  statusEl.classList.toggle("on", !!busy);
  document.body.classList.toggle("is-busy", !!busy);
  buttons.forEach(function (b) {
    if (b.getAttribute("data-keep") === "1") return;
    b.disabled = busy;
  });
}

async function pollBusy() {
  try {
    const r = await fetch("/api/status");
    const j = await r.json();
    setBusy(!!j.busy);
    applyTally(j.tally);
    document.getElementById("headless").checked = !!j.headless;
  } catch (_) {}
}

document.getElementById("headless").onchange = function (e) {
  fetch("/api/headless", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ headless: e.target.checked })
  });
};

const es = new EventSource("/api/events");
es.onmessage = function (e) {
  appendLog(e.data);
  if (/\\] (Starting:|Done\\.|Error:)/.test(e.data)) {
    pollBusy();
    loadAccounts();
  }
};

function requireAccounts() {
  const ids = selectedAccountIds();
  if (!ids.length) {
    appendLog("Select at least one account first.");
    return null;
  }
  return ids;
}

async function postJSON(url, body) {
  const ids = requireAccounts();
  if (!ids) return;
  const payload = Object.assign({ accountIds: ids }, body || {});
  const r = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload)
  });
  if (r.status === 409) {
    appendLog("Already running something — wait for it to finish.");
    return;
  }
  if (!r.ok) {
    appendLog("Request failed: " + (await r.text()));
    return;
  }
  setBusy(true);
}

document.getElementById("btnSelectAll").onclick = selectAll;
document.getElementById("btnSelectNone").onclick = selectNone;
document.getElementById("btnSelect20").onclick = selectTopAccounts;
document.getElementById("btnTopMinus10").onclick = function () { adjustTopCount(-10); };
document.getElementById("btnTopMinus1").onclick = function () { adjustTopCount(-1); };
document.getElementById("btnTopPlus1").onclick = function () { adjustTopCount(1); };
document.getElementById("btnTopPlus10").onclick = function () { adjustTopCount(10); };
acctFilter.oninput = renderAccountList;

document.getElementById("btnLogin").onclick = function () { postJSON("/api/login", {}); };

document.getElementById("btnOpenChrome").onclick = async function () {
  const ids = selectedAccountIds();
  if (ids.length === 1) {
    openChromeFor(ids[0]);
    return;
  }
  appendLog("Click Watch on the account row you want to watch.");
};

async function openChromeFor(id) {
  if (!id) {
    appendLog("Pick an account first.");
    return;
  }
  appendLog("Opening Chrome for " + id + "…");
  const r = await fetch("/api/open-chrome", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ accountIds: [id] })
  });
  if (!r.ok) {
    appendLog("Request failed: " + (await r.text()));
  }
}

document.getElementById("btnSetup").onclick = function () { postJSON("/api/setup", {}); };
document.getElementById("btnSetupSkip").onclick = async function () {
  const r = await fetch("/api/setup-skip", { method: "POST" });
  if (!r.ok) {
    appendLog("Skip failed: " + (await r.text()));
  }
};

document.getElementById("btnReach").onclick = function () {
  postJSON("/api/follow", {
    minutes: +document.getElementById("reachMinutes").value || 90,
    parallel: +document.getElementById("reachParallel").value || 10,
    maxFollows: +document.getElementById("followCount").value,
    likePosts: +document.getElementById("likePosts").value
  });
};

document.getElementById("btnReachMid").onclick = function () {
  postJSON("/api/reach-mid-replies", {
    minutes: +document.getElementById("reachMinutes").value || 90,
    parallel: +document.getElementById("reachParallel").value || 10,
    maxFollows: +document.getElementById("followCount").value,
    likePosts: +document.getElementById("likePosts").value,
    all: true,
    watchHours: +document.getElementById("msgHours").value || 4,
    dryRun: document.getElementById("msgDry").checked
  });
};

document.getElementById("btnGrowth").onclick = function () {
  postJSON("/api/growth", {});
};

document.getElementById("btnResearch").onclick = function () {
  postJSON("/api/research", { days: 14 });
};

document.getElementById("btnMessages").onclick = function () {
  postJSON("/api/messages-reply", {
    dryRun: document.getElementById("msgDry").checked,
    watch: document.getElementById("msgWatch").checked,
    watchHours: +document.getElementById("msgHours").value || 4
  });
};

document.getElementById("btnText").onclick = function () {
  postJSON("/api/text", { text: document.getElementById("textBody").value });
};

document.getElementById("btnReblog").onclick = function () {
  postJSON("/api/reblog", {
    category: document.getElementById("reblogCat").value,
    count: +document.getElementById("reblogCount").value
  });
};

document.getElementById("btnNsfw").onclick = function () {
  postJSON("/api/like-nsfw", { count: +document.getElementById("nsfwCount").value || 1 });
};

document.getElementById("btnPhoto").onclick = async function () {
  const ids = requireAccounts();
  if (!ids) return;
  const posted = document.getElementById("postedPic").value;
  const f = document.getElementById("photoFile").files[0];
  const caption = document.getElementById("photoCaption").value || "";
  const pin = document.getElementById("photoPin").checked;
  if (posted && !f) {
    return postJSON("/api/photo-file", { file: posted, caption: caption, pin: pin });
  }
  if (!f) {
    appendLog("Pick a file from pics/posted or upload an image.");
    return;
  }
  const fd = new FormData();
  fd.append("image", f);
  fd.append("caption", caption);
  fd.append("pin", pin ? "true" : "false");
  fd.append("accountIds", JSON.stringify(ids));
  const r = await fetch("/api/photo", { method: "POST", body: fd });
  if (r.status === 409) {
    appendLog("Already running something — wait for it to finish.");
    return;
  }
  if (!r.ok) {
    appendLog("Photo upload failed: " + (await r.text()));
    return;
  }
  setBusy(true);
};

setInterval(pollBusy, 2500);
loadAccounts();
loadPostedPics();
setInterval(loadAccounts, 10000);
</script>
</body>
</html>
`
