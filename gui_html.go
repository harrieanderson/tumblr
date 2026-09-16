package main

const guiHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>Tumblr Bot</title>
<link rel="preconnect" href="https://fonts.googleapis.com" />
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
<link href="https://fonts.googleapis.com/css2?family=DM+Sans:ital,opsz,wght@0,9..40,400;0,9..40,600;0,9..40,700;1,9..40,400&family=Fraunces:opsz,wght@9..144,600&display=swap" rel="stylesheet" />
<style>
  :root {
    --bg: #1a1512;
    --panel: #261f1a;
    --ink: #f3ebe3;
    --muted: #b7a89a;
    --line: #3d322a;
    --accent: #e85d4c;
    --accent2: #f0a35e;
    --ok: #7cbc6e;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0;
    min-height: 100vh;
    font-family: "DM Sans", system-ui, sans-serif;
    color: var(--ink);
    background:
      radial-gradient(900px 500px at 10% -10%, #3a2418 0%, transparent 55%),
      radial-gradient(700px 400px at 100% 0%, #2a1c28 0%, transparent 50%),
      var(--bg);
  }
  main {
    max-width: 980px;
    margin: 0 auto;
    padding: 2rem 1.25rem 3rem;
  }
  h1 {
    font-family: "Fraunces", Georgia, serif;
    font-weight: 600;
    font-size: clamp(1.8rem, 4vw, 2.4rem);
    margin: 0 0 0.35rem;
    letter-spacing: -0.02em;
  }
  .sub { color: var(--muted); margin: 0 0 1.75rem; }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: 1rem;
  }
  section {
    background: color-mix(in srgb, var(--panel) 92%, black);
    border: 1px solid var(--line);
    border-radius: 14px;
    padding: 1rem 1.1rem 1.15rem;
  }
  section h2 {
    margin: 0 0 0.35rem;
    font-size: 1.05rem;
  }
  section p {
    margin: 0 0 0.9rem;
    color: var(--muted);
    font-size: 0.9rem;
    line-height: 1.35;
  }
  label {
    display: block;
    font-size: 0.78rem;
    color: var(--muted);
    margin-bottom: 0.25rem;
  }
  .row {
    display: flex;
    gap: 0.6rem;
    flex-wrap: wrap;
    align-items: end;
    margin-bottom: 0.75rem;
  }
  .field { flex: 1; min-width: 90px; }
  input, select, textarea {
    width: 100%;
    background: #171310;
    color: var(--ink);
    border: 1px solid var(--line);
    border-radius: 8px;
    padding: 0.55rem 0.65rem;
    font: inherit;
  }
  textarea { min-height: 88px; resize: vertical; }
  button {
    appearance: none;
    border: 0;
    border-radius: 999px;
    padding: 0.6rem 1rem;
    font: inherit;
    font-weight: 600;
    cursor: pointer;
    background: linear-gradient(135deg, var(--accent), var(--accent2));
    color: #1a100c;
  }
  button:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }
  button.ghost {
    background: transparent;
    color: var(--ink);
    border: 1px solid var(--line);
  }
  .log-wrap {
    margin-top: 1.25rem;
  }
  .log-head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 0.5rem;
  }
  .status {
    font-size: 0.85rem;
    color: var(--muted);
  }
  .status.on { color: var(--ok); }
  #log {
    height: 260px;
    overflow: auto;
    background: #120e0c;
    border: 1px solid var(--line);
    border-radius: 12px;
    padding: 0.85rem 1rem;
    font-family: ui-monospace, Consolas, monospace;
    font-size: 0.82rem;
    line-height: 1.45;
    white-space: pre-wrap;
  }
  .file-name { font-size: 0.8rem; color: var(--muted); margin-top: 0.35rem; }
  .span-all { grid-column: 1 / -1; }
  .account-meta { font-size: 0.82rem; color: var(--muted); margin-top: 0.45rem; }
  .dot {
    display: inline-block;
    width: 0.55rem;
    height: 0.55rem;
    border-radius: 50%;
    background: #6a5c52;
    margin-right: 0.35rem;
    vertical-align: middle;
  }
  .dot.on { background: var(--ok); }
</style>
</head>
<body>
<main>
  <h1>Tumblr Bot</h1>
  <p class="sub">Reach-first actions. Pick a profile (or all of them), then run one action at a time.</p>

  <div class="grid">
    <section class="span-all">
      <h2>Accounts</h2>
      <p>Each account has its own Chrome profile and cookies. Login once per profile, then run reach from one or all.</p>
      <div class="row">
        <div class="field">
          <label for="accountSel">Profile</label>
          <select id="accountSel">
            <option value="">Loading…</option>
          </select>
        </div>
        <button id="btnLoginOne">Login this</button>
        <button id="btnLoginAll" class="ghost">Login all</button>
      </div>
      <div id="accountMeta" class="account-meta"></div>
    </section>

    <section>
      <h2>Reach engage</h2>
      <p>Open posts on seed blogs and tagged searches (onlyfans, cnc, brat, bimbo slvt, …). Read the <strong>likes</strong> list, like those people, and sometimes follow. A shared list stops other accounts hitting the same person.</p>
      <div class="row">
        <div class="field">
          <label for="followCount">Follows</label>
          <input id="followCount" type="number" min="1" max="80" value="40" />
        </div>
        <div class="field">
          <label for="likePosts">Likes each</label>
          <input id="likePosts" type="number" min="1" max="10" value="4" />
        </div>
      </div>
      <button id="btnFollow">Run this account</button>
      <button id="btnFollowParallel" class="ghost">Run first 5 accounts together</button>
    </section>

    <section>
      <h2>Like NSFW</h2>
      <p>Each logged-in account hearts 1 NSFW post. Pick <strong>All enabled accounts</strong> to loop everyone.</p>
      <div class="row">
        <div class="field">
          <label for="nsfwCount">Posts to like</label>
          <input id="nsfwCount" type="number" min="1" max="40" value="1" />
        </div>
      </div>
      <button id="btnNsfw">Like NSFW</button>
    </section>

    <section>
      <h2>Reblog</h2>
      <p>Reblog cats, nature, cute, or horny posts.</p>
      <div class="row">
        <div class="field">
          <label for="reblogCat">Category</label>
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
      <button id="btnReblog">Reblog</button>
    </section>

    <section>
      <h2>Text post</h2>
      <p>Publish a text post to your blog.</p>
      <label for="textBody">Text</label>
      <textarea id="textBody" placeholder="what's on your mind"></textarea>
      <div class="row" style="margin-top:0.75rem;margin-bottom:0">
        <button id="btnText">Post text</button>
      </div>
    </section>

    <section>
      <h2>Picture post</h2>
      <p>Upload an image and optional caption.</p>
      <label for="photoFile">Image</label>
      <input id="photoFile" type="file" accept="image/*" />
      <div id="photoName" class="file-name"></div>
      <label for="photoCaption" style="margin-top:0.7rem">Caption</label>
      <input id="photoCaption" type="text" placeholder="optional" />
      <div class="row" style="margin-top:0.75rem;margin-bottom:0">
        <button id="btnPhoto">Post photo</button>
      </div>
    </section>

    <section>
      <h2>Reach session</h2>
      <p>Auto idle / niche reblogs / like→reblog→soft-follow for ~90 minutes.</p>
      <button id="btnSession" class="ghost">Start reach session</button>
    </section>
  </div>

  <div class="log-wrap">
    <div class="log-head">
      <strong>Log</strong>
      <span id="status" class="status">idle</span>
    </div>
    <div id="log"></div>
  </div>
</main>
<script>
const logEl = document.getElementById("log");
const statusEl = document.getElementById("status");
const buttons = [...document.querySelectorAll("button")];
const accountSel = document.getElementById("accountSel");
const accountMeta = document.getElementById("accountMeta");
let accountsCache = [];

function selectedAccount() {
  return accountSel && accountSel.value ? accountSel.value : "";
}

function renderAccounts(list, selected) {
  accountsCache = list || [];
  const keep = selectedAccount();
  const opts = ['<option value="all">All enabled accounts</option>'];
  accountsCache.forEach(a => {
    const mark = a.loggedIn ? "ready" : "needs login";
    opts.push('<option value="' + a.id + '">' + a.id + " — " + a.email + " (" + mark + ")</option>");
  });
  accountSel.innerHTML = opts.join("");
  if (keep === "all") {
    accountSel.value = "all";
  } else if (keep && [...accountSel.options].some(o => o.value === keep)) {
    accountSel.value = keep;
  } else if (selected && [...accountSel.options].some(o => o.value === selected)) {
    accountSel.value = selected;
  } else if (accountsCache.length) {
    const active = accountsCache.find(a => a.active);
    accountSel.value = active ? active.id : accountsCache[0].id;
  }
  updateAccountMeta();
}

function updateAccountMeta() {
  const id = selectedAccount();
  if (id === "all") {
    const n = accountsCache.filter(a => a.enabled).length;
    accountMeta.textContent = n + " enabled accounts will run one after another.";
    return;
  }
  const a = accountsCache.find(x => x.id === id);
  if (!a) {
    accountMeta.textContent = "";
    return;
  }
  const blog = a.blog ? "blog " + a.blog : "blog unknown";
  const state = a.loggedIn ? "logged in" : "not logged in yet";
  const fol = (a.followers > 0) ? " · " + a.followers + " followers" : "";
  accountMeta.innerHTML = '<span class="dot' + (a.loggedIn ? " on" : "") + '"></span>' + state + " · " + blog + fol;
}

async function loadAccounts() {
  try {
    const r = await fetch("/api/accounts");
    const j = await r.json();
    renderAccounts(j.accounts || [], j.selected);
  } catch (e) {
    accountSel.innerHTML = '<option value="">Could not load accounts</option>';
  }
}

function appendLog(line) {
  logEl.textContent += line + "\n";
  logEl.scrollTop = logEl.scrollHeight;
}

function setBusy(busy) {
  statusEl.textContent = busy ? "running…" : "idle";
  statusEl.classList.toggle("on", busy);
  buttons.forEach(b => b.disabled = busy);
}

async function pollBusy() {
  try {
    const r = await fetch("/api/status");
    const j = await r.json();
    setBusy(!!j.busy);
  } catch (_) {}
}

const es = new EventSource("/api/events");
es.onmessage = (e) => {
  appendLog(e.data);
  if (/\\] (Starting:|Done\\.|Error:)/.test(e.data)) {
    pollBusy();
    loadAccounts();
  }
};

async function postJSON(url, body) {
  const payload = Object.assign({ accountId: selectedAccount() }, body || {});
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

document.getElementById("btnLoginOne").onclick = () => {
  const id = selectedAccount();
  if (!id || id === "all") {
    return postJSON("/api/login", { all: true, accountId: "all" });
  }
  postJSON("/api/login", { accountId: id });
};
document.getElementById("btnLoginAll").onclick = () => postJSON("/api/login", { all: true, accountId: "all" });
accountSel.onchange = () => {
  updateAccountMeta();
  const id = selectedAccount();
  if (id && id !== "all") {
    fetch("/api/accounts", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id })
    }).catch(() => {});
  }
};

document.getElementById("btnFollow").onclick = () => postJSON("/api/follow", {
  maxFollows: +document.getElementById("followCount").value,
  likePosts: +document.getElementById("likePosts").value
});

document.getElementById("btnFollowParallel").onclick = () => postJSON("/api/follow", {
  maxFollows: +document.getElementById("followCount").value,
  likePosts: +document.getElementById("likePosts").value,
  parallel: 5,
  accountId: "all"
});

document.getElementById("btnNsfw").onclick = () => postJSON("/api/like-nsfw", {
  count: +document.getElementById("nsfwCount").value || 1,
  accountId: "all"
});

document.getElementById("btnReblog").onclick = () => postJSON("/api/reblog", {
  category: document.getElementById("reblogCat").value,
  count: +document.getElementById("reblogCount").value
});

document.getElementById("btnText").onclick = () => postJSON("/api/text", {
  text: document.getElementById("textBody").value
});

document.getElementById("btnSession").onclick = () => postJSON("/api/session", {});

document.getElementById("photoFile").onchange = (e) => {
  const f = e.target.files && e.target.files[0];
  document.getElementById("photoName").textContent = f ? f.name : "";
};

document.getElementById("btnPhoto").onclick = async () => {
  const f = document.getElementById("photoFile").files[0];
  if (!f) {
    appendLog("Pick an image first.");
    return;
  }
  const fd = new FormData();
  fd.append("image", f);
  fd.append("caption", document.getElementById("photoCaption").value || "");
  fd.append("accountId", selectedAccount());
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
setInterval(loadAccounts, 8000);
</script>
</body>
</html>
`
