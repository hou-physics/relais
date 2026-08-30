"use strict";
const $ = (id) => document.getElementById(id);
let me = null, channel = null, members = [], sse = null, unreadTotal = 0;

async function api(path, opts = {}) {
  const resp = await fetch(path, { headers: { "Content-Type": "application/json" }, ...opts });
  if (!resp.ok) {
    let msg = "请求失败 " + resp.status;
    try { msg = (await resp.json()).error || msg; } catch {}
    throw new Error(msg);
  }
  return resp.status === 204 ? null : resp.json();
}

function md(text) {
  return DOMPurify.sanitize(marked.parse(text || ""));
}

async function boot() {
  try {
    me = await api("/api/me");
    $("login-view").hidden = true;
    $("main-view").hidden = false;
    $("me").textContent = me.display_name + "（" + me.username + "）";
    await loadChannels();
  } catch {
    $("login-view").hidden = false;
    $("main-view").hidden = true;
  }
}

$("login-btn").addEventListener("click", async () => {
  try {
    await api("/api/login", { method: "POST", body: JSON.stringify({
      username: $("login-user").value.trim(), password: $("login-pass").value }) });
    location.reload();
  } catch (e) {
    $("login-err").textContent = e.message; $("login-err").hidden = false;
  }
});
$("login-pass").addEventListener("keydown", (e) => { if (e.key === "Enter") $("login-btn").click(); });

async function loadChannels() {
  const list = await api("/api/channels");
  const nav = $("channel-list");
  nav.innerHTML = "";
  for (const ci of list) {
    const a = document.createElement("a");
    a.href = "#" + ci.name;
    a.textContent = ci.name;
    if (ci.unread > 0) {
      const b = document.createElement("span");
      b.className = "badge"; b.textContent = ci.unread; a.appendChild(b);
    }
    if (ci.name === channel) a.classList.add("active");
    a.onclick = (e) => { e.preventDefault(); openChannel(ci.name); };
    nav.appendChild(a);
  }
  if (!channel && list.length) openChannel(location.hash.slice(1) || list[0].name);
}

async function openChannel(name) {
  channel = name; location.hash = name;
  members = await api("/api/channels/" + encodeURIComponent(name) + "/members");
  renderToRow();
  await refresh();
  if (sse) sse.close();
  sse = new EventSource("/api/events?channel=" + encodeURIComponent(name));
  sse.onmessage = () => { refresh(); loadChannels(); };
  loadChannels();
}

function renderToRow() {
  const row = $("to-row");
  row.innerHTML = "";
  const others = members.filter((m) => m.username !== me.username);
  const sendBtn = document.querySelector("#composer button[type=submit]");
  if (others.length === 0) {
    row.textContent = "频道里目前只有你自己——用邀请链接把同伴拉进来后，这里会出现收件人。";
    if (sendBtn) sendBtn.disabled = true;
    return;
  }
  if (sendBtn) sendBtn.disabled = false;
  row.append("收件人：");
  for (const m of others) {
    const label = document.createElement("label");
    label.style.flexDirection = "row";
    const cb = document.createElement("input");
    cb.type = "checkbox"; cb.value = m.username;
    cb.checked = others.length === 1; // 双人频道默认对方
    label.append(cb, " " + m.display_name);
    row.appendChild(label);
  }
}

async function refresh() {
  const msgs = await api("/api/channels/" + encodeURIComponent(channel) + "/messages");
  const tl = $("timeline");
  tl.innerHTML = "";
  unreadTotal = 0;
  for (const m of msgs) {
    if (m.unread) unreadTotal++;
    tl.appendChild(renderMsg(m));
  }
  document.title = unreadTotal > 0 ? "(" + unreadTotal + ") Relais" : "Relais";
  tl.lastElementChild?.scrollIntoView({ block: "end" });
}

function renderMsg(m) {
  const div = document.createElement("div");
  div.className = "msg" + (m.from === me.username ? " mine" : "");
  div.dataset.id = m.id;
  const head = document.createElement("div");
  head.className = "head";
  head.innerHTML = `<span class="from"></span><span class="to"></span><time></time>`;
  head.querySelector(".from").textContent = m.from_display;
  head.querySelector(".to").textContent = "→ " + m.to.join(", ");
  head.querySelector("time").textContent = new Date(m.created_at).toLocaleString("zh-CN");
  div.append(head);
  if (m.in_reply_to) {
    const replyBtn = document.createElement("button");
    replyBtn.className = "toggle";
    replyBtn.textContent = "↩ 回复 " + m.in_reply_to.slice(0, 8) + "…";
    replyBtn.onclick = () => {
      document.querySelector('.msg[data-id="' + m.in_reply_to + '"]')?.scrollIntoView({ behavior: "smooth", block: "center" });
    };
    div.append(replyBtn);
  }
  const sum = document.createElement("div");
  sum.className = "summary"; sum.textContent = m.summary;
  const toggle = document.createElement("button");
  toggle.className = "toggle"; toggle.textContent = "展开正文 ▾";
  const body = document.createElement("div");
  body.className = "body"; body.hidden = true;
  toggle.onclick = async () => {
    if (body.hidden) {
      if (!body.innerHTML) {
        const full = await api("/api/messages/" + m.id);
        body.innerHTML = full.body_md ? md(full.body_md) : "<p class='muted'>（无正文）</p>";
      }
      body.hidden = false; toggle.textContent = "收起 ▴";
    } else { body.hidden = true; toggle.textContent = "展开正文 ▾"; }
  };
  div.append(sum, toggle, body);
  return div;
}

function loadFileIntoBody(file) {
  if (!file) return;
  if (!/\.(md|markdown|txt)$/i.test(file.name) && !file.type.startsWith("text/")) {
    $("send-err").textContent = "只支持 Markdown / 文本文件（.md / .txt）";
    return;
  }
  const reader = new FileReader();
  reader.onload = () => {
    const body = $("body");
    body.value = body.value ? body.value + "\n\n" + reader.result : reader.result;
    $("send-err").textContent = "";
  };
  reader.readAsText(file);
}

$("md-file").addEventListener("change", (e) => {
  loadFileIntoBody(e.target.files[0]);
  e.target.value = "";
});
const bodyEl = $("body");
bodyEl.addEventListener("dragover", (e) => { e.preventDefault(); bodyEl.classList.add("dragging"); });
bodyEl.addEventListener("dragleave", () => bodyEl.classList.remove("dragging"));
bodyEl.addEventListener("drop", (e) => {
  e.preventDefault();
  bodyEl.classList.remove("dragging");
  loadFileIntoBody(e.dataTransfer.files[0]);
});

$("composer").addEventListener("submit", async (e) => {
  e.preventDefault();
  const to = [...$("to-row").querySelectorAll("input:checked")].map((c) => c.value);
  try {
    await api("/api/channels/" + encodeURIComponent(channel) + "/messages", {
      method: "POST",
      body: JSON.stringify({ to, summary: $("summary").value, body_md: $("body").value }),
    });
    $("summary").value = ""; $("body").value = ""; $("send-err").textContent = "";
    refresh();
  } catch (err) { $("send-err").textContent = err.message; }
});

boot();
