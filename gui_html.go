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
    max-width: 1440px;
    margin: 0 auto;
    padding: 28px 28px 36px;
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
    grid-template-columns: minmax(420px, 460px) minmax(0, 1fr);
    gap: 16px;
    align-items: start;
  }
  @media (max-width: 980px) {
    .layout { grid-template-columns: 1fr; }
  }
  .card {
    position: relative;
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 26px;
    padding: 18px 18px 16px;
    box-shadow: var(--shadow);
    backdrop-filter: blur(22px);
    overflow: hidden;
  }
  .card.accounts-panel {
    padding: 18px 16px 16px;
    min-width: 0;
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
  .acct-head {
    margin-bottom: 12px;
  }
  .acct-head .hint { margin: 6px 0 0; }
  .acct-toolbar { margin: 0 0 10px; }
  .acct-tools { display: flex; gap: 6px; flex-wrap: wrap; margin-bottom: 10px; }
  .acct-list {
    height: min(62vh, 580px);
    overflow: auto;
    background: rgba(0,0,0,.22);
    border: 1px solid var(--line);
    border-radius: 18px;
    padding: 8px;
  }
  .acct-list::-webkit-scrollbar { width: 10px; }
  .acct-list::-webkit-scrollbar-thumb {
    background: rgba(255,255,255,.12);
    border-radius: 99px;
    border: 2px solid transparent;
    background-clip: padding-box;
  }
  .acct-divider {
    display: flex;
    align-items: center;
    gap: 16px;
    margin: 18px 12px 12px;
    font-size: 11px;
    letter-spacing: .24em;
    text-transform: uppercase;
  }
  .acct-divider:first-child { margin-top: 6px; }
  .acct-divider::before,
  .acct-divider::after {
    content: "";
    height: 1px;
    flex: 1;
    background: linear-gradient(90deg, transparent, rgba(232,213,181,.28), transparent);
  }
  .acct-divider span {
    color: var(--gold);
    white-space: nowrap;
  }
  .acct-divider.needs::before,
  .acct-divider.needs::after {
    background: linear-gradient(90deg, transparent, rgba(255,255,255,.1), transparent);
  }
  .acct-divider.needs span { color: var(--muted); }
  .acct {
    position: relative;
    display: grid;
    grid-template-columns: 18px 40px minmax(0, 1fr) auto;
    align-items: center;
    column-gap: 12px;
    padding: 12px 12px 12px 14px;
    border-radius: 16px;
    margin-bottom: 6px;
    border: 1px solid rgba(255,255,255,.05);
    background: rgba(255,255,255,.025);
    transition: background .18s ease, border-color .18s ease;
  }
  .acct:hover {
    background: rgba(196, 181, 253, .07);
    border-color: rgba(232, 213, 181, .18);
  }
  .acct.is-zoey {
    background: linear-gradient(105deg, rgba(232, 213, 181, .08), rgba(255,255,255,.02) 50%);
    border-color: rgba(232, 213, 181, .14);
  }
  .acct.is-zoey::before {
    content: "";
    position: absolute;
    left: 0;
    top: 11px;
    bottom: 11px;
    width: 3px;
    border-radius: 0 3px 3px 0;
    background: linear-gradient(180deg, #f4ead8, #c4b5fd);
  }
  .acct input {
    accent-color: #c4b5fd;
    width: 15px;
    height: 15px;
    margin: 0;
    justify-self: center;
  }
  .avatar {
    width: 40px;
    height: 40px;
    border-radius: 13px;
    display: grid;
    place-items: center;
    font-size: 14px;
    font-weight: 700;
    color: #1b1227;
    background: linear-gradient(160deg, #f3e7d3 0%, #e8d5b5 42%, #c4b5fd 100%);
    box-shadow: 0 6px 16px rgba(0,0,0,.28), inset 0 1px 0 rgba(255,255,255,.55);
    cursor: pointer;
  }
  .acct-body {
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
    cursor: pointer;
  }
  .acct-title {
    display: flex;
    align-items: baseline;
    gap: 8px;
    min-width: 0;
  }
  .acct-num {
    flex: 0 0 auto;
    font-size: 11px;
    font-weight: 700;
    letter-spacing: .06em;
    color: var(--gold);
    opacity: .9;
  }
  .acct-title strong {
    min-width: 0;
    font-size: 14px;
    font-weight: 620;
    letter-spacing: -.02em;
    color: var(--ink);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .acct .meta {
    color: var(--muted);
    font-size: 11px;
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: nowrap;
    min-width: 0;
    overflow: hidden;
  }
  .acct .meta > span:not(.dot):not(.sep):not(.chip) {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .acct .meta .sep {
    width: 3px;
    height: 3px;
    border-radius: 50%;
    background: rgba(255,255,255,.28);
    flex: 0 0 auto;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    flex: 0 0 auto;
    padding: 2px 7px;
    border-radius: 999px;
    font-size: 9px;
    letter-spacing: .12em;
    text-transform: uppercase;
    white-space: nowrap;
    border: 1px solid var(--line);
    background: rgba(255,255,255,.04);
    color: var(--muted);
  }
  .chip.zoey {
    color: #1a1224;
    border-color: transparent;
    background: linear-gradient(180deg, #f4ead8, #e8d5b5);
    font-weight: 700;
  }
  .chip.fol {
    color: var(--ink);
    border-color: rgba(255,255,255,.08);
    letter-spacing: .04em;
    text-transform: none;
    font-size: 10px;
  }
  .chip.need {
    color: var(--danger);
    border-color: rgba(251, 113, 133, .35);
    background: rgba(251, 113, 133, .08);
  }
  .chip.ok-sess {
    color: var(--ok);
    border-color: rgba(94, 234, 212, .28);
    background: rgba(94, 234, 212, .06);
  }
  .job-pill {
    display: none;
    max-width: 280px;
    padding: 8px 12px;
    border-radius: 14px;
    border: 1px solid var(--line);
    background: rgba(255,255,255,.03);
    color: var(--muted);
    font-size: 11px;
    letter-spacing: .04em;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .job-pill.on { display: block; color: var(--gold); border-color: var(--line2); }
  .btn-stop {
    background: transparent;
    color: var(--danger);
    border: 1px solid rgba(251, 113, 133, .45);
    box-shadow: none;
  }
  .btn-stop:hover {
    background: rgba(251, 113, 133, .12);
    border-color: rgba(251, 113, 133, .7);
  }
  .btn-stop:disabled { opacity: .35; }
  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: #6b6578;
    flex: 0 0 auto;
  }
  .dot.live { background: var(--ok); box-shadow: 0 0 8px var(--ok); }
  .acct-actions {
    display: flex;
    flex-direction: column;
    gap: 6px;
    align-items: stretch;
    flex: 0 0 auto;
    justify-self: end;
    min-width: 76px;
  }
  .acct .btn-open-one {
    padding: 7px 10px;
    font-size: 10px;
    letter-spacing: .1em;
    text-transform: uppercase;
    box-shadow: none;
    white-space: nowrap;
    flex: 0 0 auto;
    border-radius: 999px;
    background: transparent;
    color: var(--gold);
    border: 1px solid rgba(232, 213, 181, .3);
  }
  .acct .btn-open-one:hover {
    background: rgba(232, 213, 181, .1);
    border-color: rgba(232, 213, 181, .5);
  }
  .acct .btn-rm {
    width: 100%;
    height: 26px;
    padding: 0;
    border-radius: 9px;
    font-size: 15px;
    line-height: 1;
    letter-spacing: 0;
    text-transform: none;
    box-shadow: none;
    flex: 0 0 auto;
    background: transparent;
    color: var(--muted);
    border: 1px solid transparent;
    opacity: .4;
  }
  .acct:hover .btn-rm,
  .acct .btn-rm.armed { opacity: 1; }
  .acct .btn-rm:hover {
    color: var(--ink);
    border-color: var(--line);
    background: rgba(255,255,255,.04);
  }
  .acct .btn-rm.armed {
    color: var(--danger);
    border-color: rgba(248, 113, 113, .5);
    background: rgba(248, 113, 113, .1);
    font-weight: 700;
  }
  .acct-foot {
    display: flex;
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
    margin-top: 12px;
  }
  .count { margin: 0; color: var(--muted); font-size: 12px; }
  .grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 14px;
  }
  @media (max-width: 860px) {
    .grid { grid-template-columns: 1fr; }
  }
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
        <p class="sub">Local control panel over real Chrome profiles — not an embedded Tumblr login.</p>
      </div>
    </div>
    <div class="top-right">
      <div id="jobPill" class="job-pill" title="Active job"></div>
      <div id="tally" class="tally">
        <div class="tally-new zero" id="tallyNew">+0</div>
        <div>
          <div class="tally-kicker" id="tallyCaption">new today</div>
          <div class="tally-total" id="tallyTotal">0 followers</div>
        </div>
      </div>
      <label class="check" title="Bot Chrome stays hidden, including logins. Captchas can't be solved while this is on."><input id="headless" type="checkbox" /> Hide Chrome</label>
      <button type="button" id="btnStop" class="btn-stop" data-keep="1" disabled title="Cancel the current job after in-flight work">Stop</button>
      <div id="status" class="status"><i></i><span id="statusText">Idle</span></div>
    </div>
  </div>

  <div class="layout">
    <section class="card accounts-panel">
      <div class="acct-head">
        <h2>Accounts</h2>
        <p class="hint">Zoey-ready first, then by followers.</p>
      </div>
      <div class="acct-toolbar">
        <div class="field grow" style="margin:0">
          <label for="acctFilter">Filter</label>
          <input id="acctFilter" type="text" placeholder="name, email, blog" />
        </div>
      </div>
      <div class="acct-tools">
        <button type="button" id="btnSelectAll" data-keep="1">Select all</button>
        <button type="button" id="btnSelectNone" class="ghost" data-keep="1">None</button>
        <button type="button" id="btnTopMinus10" class="ghost" data-keep="1" aria-label="Decrease top count by 10">−10</button>
        <button type="button" id="btnTopMinus1" class="ghost" data-keep="1" aria-label="Decrease top count by 1">−1</button>
        <button type="button" id="btnSelect20" data-keep="1">Select top 20</button>
        <button type="button" id="btnTopPlus1" class="ghost" data-keep="1" aria-label="Increase top count by 1">+1</button>
        <button type="button" id="btnTopPlus10" class="ghost" data-keep="1" aria-label="Increase top count by 10">+10</button>
      </div>
      <div id="acctList" class="acct-list"></div>
      <div class="acct-foot">
        <div id="acctCount" class="count">0 selected</div>
        <div class="acct-tools" style="margin:0">
          <button type="button" id="btnLogin">Login selected</button>
          <button type="button" id="btnRecover" class="ghost" title="Open Chrome for accounts that need captcha/login">Recover</button>
          <button type="button" id="btnOpenChrome" class="ghost" data-keep="1">Open Chrome</button>
        </div>
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
            <input id="reachParallel" type="number" min="1" max="8" value="4" title="Capped at 8 — Chrome/Tumblr rate limits dominate" />
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
    const az = a.zoeySetup ? 1 : 0;
    const bz = b.zoeySetup ? 1 : 0;
    if (az !== bz) return bz - az; // Zoey-ified at the top
    const df = (b.followers || 0) - (a.followers || 0);
    if (df !== 0) return df;
    return String(a.id || "").toLowerCase().localeCompare(String(b.id || "").toLowerCase());
  });
}

function renderAccountList() {
  const q = (acctFilter.value || "").toLowerCase().trim();
  const html = [];
  let sawZoey = false;
  let sawRest = false;
  accountsByFollowers().forEach(function (a) {
    if (!a.enabled) return;
    const hay = ((a.id || "") + " " + (a.email || "") + " " + (a.blog || "")).toLowerCase();
    if (q && hay.indexOf(q) < 0) return;
    if (a.zoeySetup && !sawZoey) {
      sawZoey = true;
      html.push('<div class="acct-divider"><span>Zoey ready</span></div>');
    }
    if (!a.zoeySetup && !sawRest) {
      sawRest = true;
      html.push('<div class="acct-divider needs"><span>Needs setup</span></div>');
    }
    let mark = a.loggedIn ? "Logged in" : "Needs login";
    if (a.needsInteractiveLogin) mark = "Needs human";
    const title = a.blog ? "@" + a.blog : a.id;
    const num = a.num > 0 ? a.num : "";
    const tip = (num ? "#" + num + " · " : "") + (a.blog ? (title + " · " + a.id) : title) +
      (a.proxyId ? " · proxy " + a.proxyId : "") +
      (a.validatedAt ? " · ok " + a.validatedAt : "");
    const checked = selectedIds[a.id] ? " checked" : "";
    const initial = escapeHtml(((a.blog || a.id || "?").replace(/^@/, "").charAt(0) || "?").toUpperCase());
    const live = (a.loggedIn && !a.needsInteractiveLogin) ? " live" : "";
    const zoeyClass = a.zoeySetup ? " is-zoey" : "";
    const cid = "acct-" + escapeHtml(a.id);
    const numBit = num ? '<span class="acct-num">#' + num + '</span>' : "";
    const metaBits = [
      '<span class="dot' + live + '"></span>',
      '<span>' + mark + '</span>'
    ];
    if (a.needsInteractiveLogin) {
      metaBits.push('<span class="chip need">Recover</span>');
    } else if (a.loggedIn && a.validatedAt) {
      metaBits.push('<span class="chip ok-sess">OK</span>');
    }
    if (a.followers) {
      metaBits.push('<span class="chip fol">' + escapeHtml(String(a.followers)) + '</span>');
    }
    if (a.zoeySetup) {
      metaBits.push('<span class="chip zoey">Zoey</span>');
    }
    html.push(
      '<div class="acct' + zoeyClass + '" title="' + escapeHtml(tip) + '">' +
        '<input id="' + cid + '" type="checkbox" data-id="' + escapeHtml(a.id) + '"' + checked + ' />' +
        '<label class="avatar" for="' + cid + '">' + initial + '</label>' +
        '<label class="acct-body" for="' + cid + '">' +
          '<div class="acct-title">' + numBit + '<strong>' + escapeHtml(title) + '</strong></div>' +
          '<div class="meta">' + metaBits.join('') + '</div>' +
        '</label>' +
        '<div class="acct-actions">' +
          '<button type="button" class="btn-open-one" data-id="' + escapeHtml(a.id) + '" data-keep="1">Watch</button>' +
          '<button type="button" class="btn-rm" data-id="' + escapeHtml(a.id) + '" data-keep="1" title="Remove profile" aria-label="Remove profile">−</button>' +
        '</div>' +
      '</div>'
    );
  });
  acctList.innerHTML = html.join("") || '<div class="acct" style="display:flex;justify-content:center;color:var(--muted)">No accounts match.</div>';
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
  acctList.querySelectorAll(".btn-rm").forEach(function (btn) {
    btn.onclick = function (ev) {
      ev.preventDefault();
      ev.stopPropagation();
      if (!btn.classList.contains("armed")) {
        acctList.querySelectorAll(".btn-rm.armed").forEach(function (other) {
          other.classList.remove("armed");
          other.textContent = "−";
        });
        btn.classList.add("armed");
        btn.textContent = "×";
        return;
      }
      removeAccounts([btn.getAttribute("data-id")]).then(function () {
        btn.classList.remove("armed");
        btn.textContent = "−";
      });
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
  else if (/Done\.|Stopped\./.test(line)) row.classList.add("ok");
  else if (/Starting:|Emergency stop/.test(line)) row.classList.add("go");
  row.textContent = line;
  logEl.appendChild(row);
  logEl.scrollTop = logEl.scrollHeight;
}

function setBusy(busy, job) {
  statusText.textContent = busy ? "Running" : "Idle";
  statusEl.classList.toggle("on", !!busy);
  document.body.classList.toggle("is-busy", !!busy);
  const pill = document.getElementById("jobPill");
  if (pill) {
    const label = (job || "").trim();
    pill.textContent = label || "";
    pill.classList.toggle("on", !!busy && !!label);
  }
  const stopBtn = document.getElementById("btnStop");
  if (stopBtn) stopBtn.disabled = !busy;
  buttons.forEach(function (b) {
    if (b.getAttribute("data-keep") === "1") return;
    b.disabled = busy;
  });
}

async function pollBusy() {
  try {
    const r = await fetch("/api/status");
    const j = await r.json();
    setBusy(!!j.busy, j.job || "");
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
  if (/\\] (Starting:|Done\\.|Stopped\\.|Error:|Emergency stop)/.test(e.data)) {
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
document.getElementById("btnRecover").onclick = function () { postJSON("/api/recover", {}); };
document.getElementById("btnStop").onclick = async function () {
  try {
    const r = await fetch("/api/stop", { method: "POST" });
    if (!r.ok) {
      appendLog("Stop failed: " + (await r.text()));
      return;
    }
    appendLog("Emergency stop requested.");
    pollBusy();
  } catch (e) {
    appendLog("Stop failed: " + e);
  }
};

async function removeAccounts(ids) {
  ids = (ids || []).filter(Boolean);
  if (!ids.length) {
    appendLog("Select at least one account to remove.");
    return;
  }
  let label = ids.length + " accounts";
  if (ids.length === 1) {
    const hit = accountsCache.find(function (a) { return a.id === ids[0]; });
    label = hit && hit.blog ? "@" + hit.blog : ids[0];
  }
  if (!window.confirm("Are you sure?\n\nRemove " + label + " from the GUI?\n\nThis deletes the login from credentials.json. Chrome profile folders stay on disk.")) {
    return;
  }
  try {
    const r = await fetch("/api/remove-accounts", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ accountIds: ids })
    });
    const j = await r.json().catch(function () { return {}; });
    if (!r.ok) {
      appendLog("Remove failed: " + (j.error || r.statusText || r.status));
      return;
    }
    (j.removed || ids).forEach(function (id) { delete selectedIds[id]; });
    if (j.accounts) {
      accountsCache = j.accounts;
      applyTally(j.tally);
      renderAccountList();
    } else {
      await loadAccounts();
    }
    appendLog("Removed " + ((j.removed && j.removed.length) || ids.length) + " profile(s).");
  } catch (e) {
    appendLog("Remove failed: " + e);
  }
}

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
    parallel: +document.getElementById("reachParallel").value || 4,
    maxFollows: +document.getElementById("followCount").value,
    likePosts: +document.getElementById("likePosts").value
  });
};

document.getElementById("btnReachMid").onclick = function () {
  postJSON("/api/reach-mid-replies", {
    minutes: +document.getElementById("reachMinutes").value || 90,
    parallel: +document.getElementById("reachParallel").value || 4,
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
