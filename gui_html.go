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
</style>
</head>
<body>
<main>
  <h1>Tumblr Bot</h1>
  <p class="sub">Reach-first actions. One at a time — live log below.</p>

  <div class="grid">
    <section>
      <h2>Reach engage</h2>
      <p>Rebloggers of horny posts first: like their posts → reblog one → soft-follow.</p>
      <div class="row">
        <div class="field">
          <label for="followCount">People</label>
          <input id="followCount" type="number" min="1" max="20" value="5" />
        </div>
        <div class="field">
          <label for="likePosts">Likes each</label>
          <input id="likePosts" type="number" min="1" max="10" value="2" />
        </div>
      </div>
      <button id="btnFollow">Run reach engage</button>
    </section>

    <section>
      <h2>Like NSFW</h2>
      <p>Browse horny search and heart posts (no follows).</p>
      <div class="row">
        <div class="field">
          <label for="nsfwCount">Posts to like</label>
          <input id="nsfwCount" type="number" min="1" max="40" value="8" />
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

function appendLog(line) {
  logEl.textContent += line + "\\n";
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
  if (/\\] (Starting:|Done\\.|Error:)/.test(e.data)) pollBusy();
};

async function postJSON(url, body) {
  const r = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body || {})
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

document.getElementById("btnFollow").onclick = () => postJSON("/api/follow", {
  maxFollows: +document.getElementById("followCount").value,
  likePosts: +document.getElementById("likePosts").value
});

document.getElementById("btnNsfw").onclick = () => postJSON("/api/like-nsfw", {
  count: +document.getElementById("nsfwCount").value
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
</script>
</body>
</html>
`
