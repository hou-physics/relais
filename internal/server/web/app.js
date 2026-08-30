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

const AVATAR_COLORS = ["#2563eb", "#0d9488", "#dc2626", "#d97706", "#475569", "#16a34a"];
function avatarNode(username, displayName, avatar) {
  const el = document.createElement("span");
  el.className = "avatar";
  if (avatar) {
    el.textContent = avatar;
    el.style.background = "transparent";
    el.style.fontSize = "20px";
  } else {
    let h = 0;
    for (const ch of username) h = (h * 31 + ch.codePointAt(0)) >>> 0;
    el.style.background = AVATAR_COLORS[h % AVATAR_COLORS.length];
    el.textContent = (displayName || username).slice(0, 1).toUpperCase();
  }
  return el;
}

// 轻量 frontmatter 解析：只认最前面的 --- 块里的 summary 行
function parseFrontmatter(text) {
  const m = text.match(/^---\r?\n([\s\S]*?)\r?\n---\r?\n?/);
  if (!m) return null;
  const line = m[1].split(/\r?\n/).find((l) => /^summary\s*:/.test(l));
  if (!line) return null;
  let summary = line.replace(/^summary\s*:\s*/, "").trim();
  if ((summary.startsWith('"') && summary.endsWith('"')) || (summary.startsWith("'") && summary.endsWith("'"))) {
    summary = summary.slice(1, -1);
  }
  if (!summary) return null;
  return { summary, body: text.slice(m[0].length).replace(/^\r?\n/, "") };
}

async function copyText(btn, text) {
  try {
    await navigator.clipboard.writeText(text);
    const old = btn.textContent;
    btn.textContent = "已复制 ✓";
    setTimeout(() => { btn.textContent = old; }, 1500);
  } catch {
    $("send-err").textContent = "复制失败：请检查浏览器剪贴板权限";
  }
}

function aiTemplate() {
  return [
    "请把我接下来提供的内容，整理成一个用于 Relais 的 Markdown 消息文件。格式要求：",
    "1. 文件最开头必须是三行文件头：",
    "---",
    "summary: <一两句话的摘要，给人快速浏览用>",
    "---",
    "2. 之后是完整正文（给对方 AI 阅读）：可以用标题、表格、代码块，可以包含希望对方 AI 执行的具体指令。",
    "3. 摘要务必简洁准确；正文务必自包含（对方 AI 只看这一个文件）。",
    "生成后把整个文件内容发给我。",
  ].join("\n");
}

function aiInstruction(m) {
  return "请运行 relais pull 拉取消息 " + m.id + "，阅读 relais/inbox/ 下对应文件，与我讨论后用 relais draft 起草回复（不要直接 send）。";
}

async function boot() {
  try {
    me = await api("/api/me");
    $("login-view").hidden = true;
    $("main-view").hidden = false;
    const btn = $("me-btn");
    btn.innerHTML = "";
    btn.append(avatarNode(me.username, me.display_name, me.avatar));
    $("menu-name").textContent = me.display_name + "（" + me.username + "）";
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

async function loadDrafts() {
  const box = $("drafts");
  box.innerHTML = "";
  let drafts = [];
  try { drafts = await api("/api/channels/" + encodeURIComponent(channel) + "/drafts"); } catch { return; }
  for (const d of drafts) {
    const card = document.createElement("div");
    card.className = "draft-card";
    const tag = document.createElement("span");
    tag.className = "draft-tag"; tag.textContent = "草稿";
    const meta = document.createElement("span");
    meta.className = "muted"; meta.textContent = "→ " + d.to.join(", ");
    const sum = document.createElement("div");
    sum.className = "summary"; sum.textContent = d.summary;
    const body = document.createElement("div");
    body.className = "body"; body.hidden = true;
    const toggle = document.createElement("button");
    toggle.className = "toggle"; toggle.textContent = "展开正文 ▾";
    toggle.onclick = () => {
      if (body.hidden) { body.innerHTML = d.body_md ? md(d.body_md) : "<p class='muted'>（无正文）</p>"; body.hidden = false; toggle.textContent = "收起 ▴"; }
      else { body.hidden = true; toggle.textContent = "展开正文 ▾"; }
    };
    const send = document.createElement("button");
    send.textContent = "发送";
    send.onclick = async () => {
      try { await api("/api/drafts/" + d.id + "/send", { method: "POST" }); loadDrafts(); refresh(); }
      catch (e) { $("send-err").textContent = e.message; }
    };
    const del = document.createElement("button");
    del.className = "ghost"; del.textContent = "删除";
    del.onclick = async () => { await api("/api/drafts/" + d.id, { method: "DELETE" }); loadDrafts(); };
    const row = document.createElement("div");
    row.className = "row";
    row.append(toggle, send, del);
    card.append(tag, meta, sum, body, row);
    box.append(card);
  }
}

async function openChannel(name) {
  channel = name; location.hash = name;
  members = await api("/api/channels/" + encodeURIComponent(name) + "/members");
  renderToRow();
  loadDrafts();
  await refresh();
  if (sse) sse.close();
  sse = new EventSource("/api/events?channel=" + encodeURIComponent(name));
  sse.onmessage = () => { refresh(); loadChannels(); loadDrafts(); };
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
    label.append(cb, avatarNode(m.username, m.display_name, m.avatar || ""));
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
  head.prepend(avatarNode(m.from, m.from_display, m.from_avatar || ""));
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
  const body = document.createElement("div");
  body.className = "body"; body.hidden = true;
  const actions = document.createElement("div");
  actions.className = "actions";
  const toggle = document.createElement("button");
  toggle.className = "toggle"; toggle.textContent = "展开正文 ▾";
  toggle.onclick = async () => {
    if (body.hidden) {
      if (!body.innerHTML) {
        const full = await api("/api/messages/" + m.id);
        body.innerHTML = full.body_md ? md(full.body_md) : "<p class='muted'>（无正文）</p>";
      }
      body.hidden = false; toggle.textContent = "收起 ▴";
    } else { body.hidden = true; toggle.textContent = "展开正文 ▾"; }
  };
  actions.append(toggle);
  const copyRaw = document.createElement("button");
  copyRaw.className = "toggle"; copyRaw.textContent = "复制原文";
  copyRaw.onclick = async () => {
    const full = await api("/api/messages/" + m.id);
    const head = ["---", "id: " + full.id, "channel: " + full.channel, "from: " + full.from,
      "to: [" + full.to.join(", ") + "]", "sent: " + full.created_at, "summary: " + full.summary, "---", ""].join("\n");
    copyText(copyRaw, head + (full.body_md || ""));
  };
  actions.append(copyRaw);
  if (m.to.includes(me.username)) {
    const feed = document.createElement("button");
    feed.className = "toggle"; feed.textContent = "复制给 AI 的指令";
    feed.onclick = () => copyText(feed, aiInstruction(m));
    actions.append(feed);
    if (m.unread) {
      const markRead = document.createElement("button");
      markRead.className = "toggle"; markRead.textContent = "标记已读";
      markRead.onclick = async () => {
        await api("/api/messages/" + m.id + "/read", { method: "POST" });
        refresh(); loadChannels();
      };
      actions.append(markRead);
    }
  }
  div.append(sum, actions, body);
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
    let text = reader.result;
    const fm = parseFrontmatter(text);
    if (fm) {
      if (!$("summary").value) $("summary").value = fm.summary;
      text = fm.body;
    }
    const body = $("body");
    body.value = body.value ? body.value + "\n\n" + text : text;
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
    refresh(); loadDrafts();
  } catch (err) { $("send-err").textContent = err.message; }
});

$("tpl-btn").addEventListener("click", () => copyText($("tpl-btn"), aiTemplate()));

$("me-btn").addEventListener("click", () => { $("user-menu").hidden = !$("user-menu").hidden; });
document.addEventListener("click", (e) => {
  if (!$("user-menu").hidden && !$("user-menu").contains(e.target) && e.target !== $("me-btn") && !$("me-btn").contains(e.target)) {
    $("user-menu").hidden = true;
  }
});
function showView(name) {
  $("chat-view").hidden = name !== "chat";
  $("settings-view").hidden = name !== "settings";
  $("user-menu").hidden = true;
}
$("menu-settings").addEventListener("click", () => {
  $("set-display").value = me.display_name;
  $("set-avatar").value = me.avatar || "";
  showView("settings");
});
$("back-chat").addEventListener("click", () => showView("chat"));
$("menu-logout").addEventListener("click", async () => {
  try { await api("/api/logout", { method: "POST" }); } catch {}
  location.reload();
});
$("save-profile").addEventListener("click", async () => {
  try {
    me = await api("/api/settings/profile", { method: "POST",
      body: JSON.stringify({ display_name: $("set-display").value.trim(), avatar: $("set-avatar").value.trim() }) });
    $("profile-msg").textContent = "✓";
    setTimeout(() => { $("profile-msg").textContent = ""; }, 1500);
    const btn = $("me-btn");
    btn.innerHTML = "";
    btn.append(avatarNode(me.username, me.display_name, me.avatar));
  } catch (e) { $("profile-msg").textContent = e.message; }
});
$("save-pw").addEventListener("click", async () => {
  try {
    await api("/api/settings/password", { method: "POST",
      body: JSON.stringify({ old: $("pw-old").value, new: $("pw-new").value }) });
    $("pw-old").value = ""; $("pw-new").value = "";
    $("pw-msg").className = "ok"; $("pw-msg").textContent = "✓";
  } catch (e) { $("pw-msg").className = "err"; $("pw-msg").textContent = e.message; }
});
$("regen-token").addEventListener("click", async () => {
  try {
    const tr = await api("/api/settings/token", { method: "POST" });
    const out = $("token-out");
    out.hidden = false;
    out.textContent = "relais login " + location.origin + " --token " + tr.agent_token;
  } catch (e) { $("pw-msg").textContent = e.message; }
});

boot();
