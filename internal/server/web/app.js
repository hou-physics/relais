"use strict";
const $ = (id) => document.getElementById(id);
let me = null, channel = null, members = [], sse = null, unreadTotal = 0;

// Supported languages: 中文 (zh), English (en), Deutsch (de)
const I18N = {
  zh: {
    tagline: "agent 之间的信使 · 人在回路", username: "用户名", password: "密码", login: "登录",
    settings: "个人设置", logout: "退出登录", send: "发送", importFile: "导入 Markdown 文件",
    copyTpl: "复制 ChatGPT 协议模板", summaryPh: "摘要（给人看的一两句话，必填）",
    bodyPh: "正文 Markdown（给对方 agent 看的完整内容，可留空；可直接把 .md 文件拖进这里）",
    displayName: "显示名", avatarEmoji: "头像（一个 emoji，留空用首字母）", save: "保存",
    changePw: "修改密码", oldPw: "旧密码", newPw: "新密码（至少 8 位）",
    tokenHint: "重置后旧 token 立即失效，需要在电脑上重新运行 relais login。", regenToken: "重置 agent token",
    notify: "桌面通知", notifyHint: "页面在后台时，新消息弹系统通知。", notifyEnable: "启用桌面通知",
    notifyOn: "已启用 ✓", notifyDenied: "浏览器已拒绝（请在浏览器设置里允许）", backChat: "← 返回聊天",
    expand: "展开正文 ▾", collapse: "收起 ▴", copyRaw: "复制原文", feedAI: "复制给 AI 的指令",
    markRead: "标记已读", copied: "已复制 ✓", copyFail: "复制失败：请检查浏览器剪贴板权限", draft: "草稿", del: "删除",
    soloHint: "频道里目前只有你自己——用邀请链接把同伴拉进来后，这里会出现收件人。",
    recipients: "收件人：", noBody: "（无正文）", unreadPrefix: "未读",
    channelAdmin: "频道管理", createChannel: "新建频道", newChannelPh: "新频道名（小写字母开头）",
    members: "成员", addMember: "添加成员", addMemberPh: "已注册用户名", genInvite: "生成邀请链接",
    remove: "移除",
    pause: "暂停", resume: "继续", guideMyAgent: "给我的 agent 说一句",
    autoRunning: "自主对话中（第 {n}/{cap} 轮）", autoPaused: "已暂停", autoNeedsYou: "需要你回答：",
    autoOff: "自主对话：未开启", autoEnable: "开启自主对话", autoDisable: "关闭自主对话", autoCap: "上限回合",
    guidePrompt: "给你自己的 agent 一句私下引导（对方看不到）：",
  },
  en: {
    tagline: "Messenger between agents · human in the loop", username: "Username", password: "Password", login: "Sign in",
    settings: "Settings", logout: "Sign out", send: "Send", importFile: "Import Markdown file",
    copyTpl: "Copy ChatGPT protocol", summaryPh: "Summary (one or two sentences for humans, required)",
    bodyPh: "Markdown body (full content for the other agent; optional; drag a .md file here)",
    displayName: "Display name", avatarEmoji: "Avatar (one emoji; empty = initial)", save: "Save",
    changePw: "Change password", oldPw: "Old password", newPw: "New password (min. 8 chars)",
    tokenHint: "Resetting invalidates the old token immediately; run relais login again on your computer.", regenToken: "Reset agent token",
    notify: "Desktop notifications", notifyHint: "Get a system notification for new messages while the tab is in background.", notifyEnable: "Enable notifications",
    notifyOn: "Enabled ✓", notifyDenied: "Blocked by browser (allow it in browser settings)", backChat: "← Back to chat",
    expand: "Show body ▾", collapse: "Hide ▴", copyRaw: "Copy raw", feedAI: "Copy AI instruction",
    markRead: "Mark read", copied: "Copied ✓", copyFail: "Copy failed — check the browser's clipboard permission", draft: "DRAFT", del: "Delete",
    soloHint: "You are the only member so far — invite your partner and recipients will appear here.",
    recipients: "To: ", noBody: "(no body)", unreadPrefix: "Unread",
    channelAdmin: "Channel admin", createChannel: "Create channel", newChannelPh: "New channel name (lowercase start)",
    members: "Members", addMember: "Add member", addMemberPh: "Registered username", genInvite: "Generate invite link",
    remove: "Remove",
    pause: "Pause", resume: "Resume", guideMyAgent: "Tell my agent",
    autoRunning: "Auto-chat running ({n}/{cap})", autoPaused: "Paused", autoNeedsYou: "Needs your answer:",
    autoOff: "Auto-chat: off", autoEnable: "Enable auto-chat", autoDisable: "Disable", autoCap: "Round cap",
    guidePrompt: "Private guidance to your own agent (the other side won't see it):",
  },
  de: {
    tagline: "Bote zwischen Agents · Mensch in der Schleife", username: "Benutzername", password: "Passwort", login: "Anmelden",
    settings: "Einstellungen", logout: "Abmelden", send: "Senden", importFile: "Markdown-Datei importieren",
    copyTpl: "ChatGPT-Protokoll kopieren", summaryPh: "Zusammenfassung (ein bis zwei Sätze für Menschen, Pflicht)",
    bodyPh: "Markdown-Inhalt (vollständiger Text für den anderen Agent; optional; .md-Datei hierher ziehen)",
    displayName: "Anzeigename", avatarEmoji: "Avatar (ein Emoji; leer = Initiale)", save: "Speichern",
    changePw: "Passwort ändern", oldPw: "Altes Passwort", newPw: "Neues Passwort (mind. 8 Zeichen)",
    tokenHint: "Nach dem Zurücksetzen ist der alte Token sofort ungültig; führe relais login erneut aus.", regenToken: "Agent-Token zurücksetzen",
    notify: "Desktop-Benachrichtigungen", notifyHint: "Systembenachrichtigung bei neuen Nachrichten, wenn der Tab im Hintergrund ist.", notifyEnable: "Benachrichtigungen aktivieren",
    notifyOn: "Aktiviert ✓", notifyDenied: "Vom Browser blockiert (in den Browsereinstellungen erlauben)", backChat: "← Zurück zum Chat",
    expand: "Inhalt zeigen ▾", collapse: "Einklappen ▴", copyRaw: "Rohtext kopieren", feedAI: "KI-Anweisung kopieren",
    markRead: "Als gelesen markieren", copied: "Kopiert ✓", copyFail: "Kopieren fehlgeschlagen — Zwischenablage-Berechtigung im Browser prüfen", draft: "ENTWURF", del: "Löschen",
    soloHint: "Du bist bisher das einzige Mitglied — lade deine Partnerin ein, dann erscheinen hier Empfänger.",
    recipients: "An: ", noBody: "(kein Inhalt)", unreadPrefix: "Ungelesen",
    channelAdmin: "Kanalverwaltung", createChannel: "Kanal erstellen", newChannelPh: "Kanalname (Kleinbuchstabe zuerst)",
    members: "Mitglieder", addMember: "Mitglied hinzufügen", addMemberPh: "Registrierter Benutzername", genInvite: "Einladungslink erzeugen",
    remove: "Entfernen",
    pause: "Pause", resume: "Fortsetzen", guideMyAgent: "Meinem Agent sagen",
    autoRunning: "Auto-Chat läuft ({n}/{cap})", autoPaused: "Pausiert", autoNeedsYou: "Braucht deine Antwort:",
    autoOff: "Auto-Chat: aus", autoEnable: "Auto-Chat aktivieren", autoDisable: "Deaktivieren", autoCap: "Rundenlimit",
    guidePrompt: "Private Anweisung an deinen Agent (die andere Seite sieht sie nicht):",
  },
};
function detectLang() {
  let saved = null;
  try { saved = localStorage.getItem("relais-lang"); } catch {}
  if (saved && I18N[saved]) return saved;
  const nav = (navigator.language || "en").toLowerCase();
  if (nav.startsWith("zh")) return "zh";
  if (nav.startsWith("de")) return "de";
  return "en";
}
let lang = detectLang();
function t(key) { return (I18N[lang] && I18N[lang][key]) || I18N.zh[key] || key; }
window.__t = t;
function applyI18n() {
  document.querySelectorAll("[data-i18n]").forEach((el) => { el.textContent = t(el.dataset.i18n); });
  document.querySelectorAll("[data-i18n-placeholder]").forEach((el) => { el.placeholder = t(el.dataset.i18nPlaceholder); });
  document.documentElement.lang = lang === "zh" ? "zh-CN" : lang;
}
function localeFor(l) { return l === "zh" ? "zh-CN" : l === "de" ? "de-DE" : "en-US"; }

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
    btn.textContent = t("copied");
    setTimeout(() => { btn.textContent = old; }, 1500);
  } catch {
    $("send-err").textContent = t("copyFail");
  }
}

// 模板语言固定为中文（消息约定与 AI 交互的格式统一，不随 UI 语言变化）
function aiTemplate() {
  return [
    "我在用一个叫 Relais 的工具和同事的 AI 协作。请在我们整个对话里始终遵守：",
    "",
    "1. 我会不时把「收到的消息」粘给你，格式是：开头一段 --- ... --- 文件头",
    "   （含 from / summary 等），下面是正文。请当作对方及对方的 AI 发来的内容阅读理解。",
    "",
    "2. 当我让你写回复、或写一条要发出去的消息时，你必须输出成下面这个格式，",
    "   我会原样复制去发送：",
    "---",
    "summary: <一两句话摘要，给人快速浏览>",
    "---",
    "<正文（Markdown），给对方的 AI 阅读，可用表格、代码块、给对方 AI 的指令>",
    "",
    "3. summary 必须简洁准确；正文要自包含（对方只看这一条）。",
    "4. 默认就用上面的格式输出，不要加多余的寒暄或解释。",
  ].join("\n");
}

function aiInstruction(m) {
  return "请运行 relais pull 拉取消息 " + m.id + "，阅读 relais/inbox/ 下对应文件，与我讨论后用 relais draft 起草回复（不要直接 send）。";
}

$("lang-select").addEventListener("change", () => {
  lang = $("lang-select").value;
  try { localStorage.setItem("relais-lang", lang); } catch {}
  applyI18n();
  if (channel) { renderToRow(); refresh(); loadDrafts(); }
});

async function boot() {
  applyI18n();
  $("lang-select").value = lang;
  try {
    me = await api("/api/me");
    $("login-view").hidden = true;
    $("main-view").hidden = false;
    const btn = $("me-btn");
    btn.innerHTML = "";
    btn.append(avatarNode(me.username, me.display_name, me.avatar));
    $("menu-name").textContent = me.display_name + "（" + me.username + "）";
    $("menu-admin").hidden = !me.is_admin;
    await loadChannels();
  } catch {
    $("login-view").hidden = false;
    $("main-view").hidden = true;
  }
}

function updateNotifyBtn() {
  const btn = $("notify-btn");
  if (!("Notification" in window)) { btn.disabled = true; return; }
  if (Notification.permission === "granted") { btn.textContent = t("notifyOn"); btn.disabled = true; }
  else if (Notification.permission === "denied") { btn.textContent = t("notifyDenied"); btn.disabled = true; }
}
$("notify-btn").addEventListener("click", async () => {
  if (!("Notification" in window)) return;
  await Notification.requestPermission();
  updateNotifyBtn();
});
function maybeNotify(m) {
  if (!("Notification" in window) || Notification.permission !== "granted") return;
  if (!document.hidden) return;
  if (m.from === me.username) return;
  const n = new Notification(m.from_display, { body: m.summary });
  n.onclick = () => { window.focus(); n.close(); };
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
    tag.className = "draft-tag"; tag.textContent = t("draft");
    const meta = document.createElement("span");
    meta.className = "muted"; meta.textContent = "→ " + d.to.join(", ");
    const sum = document.createElement("div");
    sum.className = "summary"; sum.textContent = d.summary;
    const body = document.createElement("div");
    body.className = "body"; body.hidden = true;
    const toggle = document.createElement("button");
    toggle.className = "toggle"; toggle.textContent = t("expand");
    toggle.onclick = () => {
      if (body.hidden) {
        if (d.body_md) {
          body.innerHTML = md(d.body_md);
        } else {
          body.textContent = t("noBody");
        }
        body.hidden = false;
        toggle.textContent = t("collapse");
      }
      else { body.hidden = true; toggle.textContent = t("expand"); }
    };
    const send = document.createElement("button");
    send.textContent = t("send");
    send.onclick = async () => {
      try { await api("/api/drafts/" + d.id + "/send", { method: "POST" }); loadDrafts(); refresh(); }
      catch (e) { $("send-err").textContent = e.message; }
    };
    const del = document.createElement("button");
    del.className = "ghost"; del.textContent = t("del");
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
  sse.onmessage = (ev) => {
    try { maybeNotify(JSON.parse(ev.data)); } catch {}
    refresh(); loadChannels(); loadDrafts(); loadAutoState();
  };
  loadChannels();
  loadAutoState();
}

async function loadAutoState() {
  const bar = $("auto-bar");
  let st;
  try { st = await api("/api/channels/" + encodeURIComponent(channel) + "/auto"); }
  catch { bar.hidden = true; return; }
  bar.hidden = false;
  const on = !!st.enabled;
  if (on) $("auto-cap").value = st.cap;          // 输入框同步真实 cap
  $("auto-off-ctl").hidden = on;                  // 开启控件仅在 auto 关时显示
  $("auto-off").hidden = !on;                     // 关闭按钮仅在 auto 开时显示
  $("auto-guide").hidden = !on;
  $("auto-pause").hidden = !on || st.paused;
  $("auto-resume").hidden = !on || !st.paused;
  if (!on) { $("auto-state").textContent = t("autoOff"); $("auto-state").className = "muted"; return; }
  let text = t("autoRunning").replace("{n}", st.round_count).replace("{cap}", st.cap);
  if (st.needs_human_q) text = "⚠️ " + t("autoNeedsYou") + " " + st.needs_human_q;
  else if (st.paused) text = t("autoPaused");
  $("auto-state").textContent = text;
  $("auto-state").className = (st.needs_human_q || st.paused) ? "err" : "muted";
}

$("auto-on").addEventListener("click", async () => {
  const cap = parseInt($("auto-cap").value, 10) || 6;
  await api("/api/channels/" + encodeURIComponent(channel) + "/auto", { method: "POST", body: JSON.stringify({ enabled: true, cap }) });
  loadAutoState();
});
$("auto-off").addEventListener("click", async () => {
  const cap = parseInt($("auto-cap").value, 10) || 6;
  await api("/api/channels/" + encodeURIComponent(channel) + "/auto", { method: "POST", body: JSON.stringify({ enabled: false, cap }) });
  loadAutoState();
});
$("auto-pause").addEventListener("click", async () => {
  await api("/api/channels/" + encodeURIComponent(channel) + "/auto/pause", { method: "POST" });
  loadAutoState();
});
$("auto-resume").addEventListener("click", async () => {
  await api("/api/channels/" + encodeURIComponent(channel) + "/auto/resume", { method: "POST" });
  loadAutoState();
});
$("auto-guide").addEventListener("click", async () => {
  const note = prompt(t("guidePrompt"));
  if (note == null || note.trim() === "") return;
  await api("/api/channels/" + encodeURIComponent(channel) + "/guidance", { method: "POST", body: JSON.stringify({ note: note.trim() }) });
});

function renderToRow() {
  const row = $("to-row");
  const prevChecked = new Set([...row.querySelectorAll("input:checked")].map((c) => c.value));
  row.innerHTML = "";
  const others = members.filter((m) => m.username !== me.username);
  const sendBtn = document.querySelector("#composer button[type=submit]");
  if (others.length === 0) {
    row.textContent = t("soloHint");
    if (sendBtn) sendBtn.disabled = true;
    return;
  }
  if (sendBtn) sendBtn.disabled = false;
  row.append(t("recipients"));
  for (const m of others) {
    const label = document.createElement("label");
    label.style.flexDirection = "row";
    const cb = document.createElement("input");
    cb.type = "checkbox"; cb.value = m.username;
    cb.checked = prevChecked.size ? prevChecked.has(m.username) : (others.length === 1);
    label.append(cb, avatarNode(m.username, m.display_name, m.avatar || ""), " " + m.display_name);
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
  head.querySelector("time").textContent = new Date(m.created_at).toLocaleString(localeFor(lang));
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
  toggle.className = "toggle"; toggle.textContent = t("expand");
  toggle.onclick = async () => {
    if (body.hidden) {
      if (!body.innerHTML) {
        const full = await api("/api/messages/" + m.id);
        if (full.body_md) {
          body.innerHTML = md(full.body_md);
        } else {
          body.textContent = t("noBody");
        }
      }
      body.hidden = false; toggle.textContent = t("collapse");
    } else { body.hidden = true; toggle.textContent = t("expand"); }
  };
  actions.append(toggle);
  const copyRaw = document.createElement("button");
  copyRaw.className = "toggle"; copyRaw.textContent = t("copyRaw");
  copyRaw.onclick = async () => {
    const full = await api("/api/messages/" + m.id);
    const head = ["---", "id: " + full.id, "channel: " + full.channel, "from: " + full.from,
      "to: [" + full.to.join(", ") + "]", "sent: " + full.created_at, "summary: " + full.summary, "---", ""].join("\n");
    copyText(copyRaw, head + (full.body_md || ""));
  };
  actions.append(copyRaw);
  if (m.to.includes(me.username)) {
    const feed = document.createElement("button");
    feed.className = "toggle"; feed.textContent = t("feedAI");
    feed.onclick = () => copyText(feed, aiInstruction(m));
    actions.append(feed);
    if (m.unread) {
      const markRead = document.createElement("button");
      markRead.className = "toggle"; markRead.textContent = t("markRead");
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
  $("admin-view").hidden = name !== "admin";
  $("user-menu").hidden = true;
}
$("menu-settings").addEventListener("click", () => {
  $("set-display").value = me.display_name;
  $("set-avatar").value = me.avatar || "";
  showView("settings");
  updateNotifyBtn();
});
$("back-chat").addEventListener("click", () => showView("chat"));

let adminDetailChannel = null;

$("menu-admin").addEventListener("click", () => { loadAdminChannels(); showView("admin"); });
$("admin-back").addEventListener("click", () => showView("chat"));

async function loadAdminChannels() {
  const box = $("admin-channels");
  box.innerHTML = "";
  let stats = [];
  try { stats = await api("/api/admin/channels"); } catch (e) { $("admin-msg").textContent = e.message; return; }
  for (const st of stats) {
    const row = document.createElement("div");
    row.className = "row";
    const link = document.createElement("button");
    link.className = "toggle";
    link.textContent = st.name + "（" + st.members + "）";
    link.onclick = () => openChannelDetail(st.name);
    row.append(link);
    box.append(row);
  }
}

$("create-channel").addEventListener("click", async () => {
  try {
    await api("/api/admin/channels", { method: "POST", body: JSON.stringify({ name: $("new-channel").value.trim() }) });
    $("new-channel").value = ""; $("admin-msg").textContent = "";
    loadAdminChannels();
  } catch (e) { $("admin-msg").textContent = e.message; }
});

async function openChannelDetail(name) {
  adminDetailChannel = name;
  $("channel-detail").hidden = false;
  $("detail-channel").textContent = name;
  $("invite-out").hidden = true;
  await loadDetailMembers();
}

async function loadDetailMembers() {
  const box = $("detail-members");
  box.innerHTML = "";
  const ms = await api("/api/admin/channels/" + encodeURIComponent(adminDetailChannel) + "/members");
  for (const m of ms) {
    const row = document.createElement("div");
    row.className = "row";
    row.append(avatarNode(m.username, m.display_name, m.avatar || ""), " " + m.display_name + "（" + m.username + "）");
    const rm = document.createElement("button");
    rm.className = "danger"; rm.textContent = t("remove");
    rm.onclick = async () => {
      await api("/api/admin/channels/" + encodeURIComponent(adminDetailChannel) + "/members/" + encodeURIComponent(m.username), { method: "DELETE" });
      loadDetailMembers();
    };
    row.append(rm);
    box.append(row);
  }
}

$("add-member").addEventListener("click", async () => {
  try {
    await api("/api/admin/channels/" + encodeURIComponent(adminDetailChannel) + "/members", {
      method: "POST", body: JSON.stringify({ username: $("add-member-name").value.trim() }) });
    $("add-member-name").value = "";
    loadDetailMembers();
  } catch (e) { $("admin-msg").textContent = e.message; }
});

$("channel-invite").addEventListener("click", async () => {
  try {
    const out = await api("/api/admin/channels/" + encodeURIComponent(adminDetailChannel) + "/invites", { method: "POST" });
    $("invite-out").hidden = false;
    $("invite-out").textContent = out.url;
  } catch (e) { $("admin-msg").textContent = e.message; }
});
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
