"use strict";
const $ = (id) => document.getElementById(id);
let modules = [], current = null, messages = [], sse = null, settings = null;

async function api(path, opts = {}) {
  const resp = await fetch(path, { headers: { "Content-Type": "application/json" }, ...opts });
  if (resp.status === 204) return null;
  const text = await resp.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = { error: text }; }
  if (!resp.ok) throw new Error((data && data.error) || ("请求失败 " + resp.status));
  const warn = resp.headers.get("X-Relais-Error");
  if (warn && data && typeof data === "object") data.__warn = decodeURIComponent(warn.replace(/\+/g, " "));
  return data;
}
function md(text) { return DOMPurify.sanitize(marked.parse(text || "")); }
function esc(s) { return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])); }
function ago(iso) {
  if (!iso || iso.startsWith("0001")) return "";
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return "刚刚";
  if (s < 3600) return Math.floor(s / 60) + " 分钟前";
  if (s < 86400) return Math.floor(s / 3600) + " 小时前";
  return new Date(iso).toLocaleDateString("zh-CN");
}
function toast(msg) { alert(msg); }
async function copy(text) { try { await navigator.clipboard.writeText(text); toast("已复制：" + text); } catch { prompt("复制这句话：", text); } }
const attachHint = (name) => "接入 relais 模块 " + name;

// ---------- 守卫与模块列表 ----------
async function loadState() {
  try {
    const st = await api("/api/local/state");
    $("daemon-dot").classList.add("on");
    $("daemon-text").textContent = "守卫在跑 · " + st.version + (st.codex_ok ? "" : " · codex 路径未设置");
  } catch (e) {
    $("daemon-dot").classList.remove("on");
    $("daemon-text").textContent = "守卫没响应：" + e.message;
  }
}
async function loadModules() {
  modules = await api("/api/local/modules");
  $("empty").hidden = modules.length > 0;
  $("main").hidden = modules.length === 0;
  const list = $("module-list");
  list.innerHTML = "";
  for (const m of modules) {
    const el = document.createElement("div");
    el.className = "module" + (current && current.name === m.name ? " active" : "");
    const chipClass = m.state === "等你" ? "you" : (m.state === "讨论中" ? "ink" : "");
    el.innerHTML = `<div class="name">${esc(m.name)}</div>
      <div class="meta"><span class="chip ${chipClass}">${esc(m.state)}</span><span class="mono muted">${m.round}/${m.round_cap} · ${esc(ago(m.last_at)) || "无信"}</span></div>`;
    el.onclick = () => select(m.name).catch((e) => toast(e.message));
    list.appendChild(el);
  }
  if (!current && modules.length) select(modules[0].name).catch((e) => toast(e.message));
  else if (current) {
    const fresh = modules.find((m) => m.name === current.name);
    if (fresh) { current = fresh; renderNow(); }
    else { current = null; if (sse) { sse.close(); sse = null; } if (modules.length) select(modules[0].name).catch((e) => toast(e.message)); }
  }
}

// ---------- 当前模块 ----------
async function select(name) {
  current = modules.find((m) => m.name === name);
  if (!current) return;
  filledFor = null;
  document.querySelectorAll(".module").forEach((el) => el.classList.toggle("active", el.querySelector(".name").textContent === name));
  renderNow();
  await loadMessages();
  if (!current || current.name !== name) return; // 等待期间又换了模块
  openSSE();
}
const sideLabel = { claude: "Claude", codex: "Codex" };
// 被等的那一侧是否接上了：claude 看 waiting，codex 看 attached
function sideConnected(m, side) { return side === "claude" ? m.claude.waiting : side === "codex" ? m.codex.attached : false; }
function sideText(m) {
  const c = m.claude, x = m.codex;
  const claude = c.waiting ? `在等信${c.session_name ? " · 对话「" + esc(c.session_name) + "」" : ""} · 从 ${esc(new Date(c.wait_since).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" }))} 起`
    : `没在等 · 在 Claude 对话里说：${esc(attachHint(m.name))}`;
  let codex = x.attached ? `已接入「${esc(x.thread_name || "未命名对话")}」` : `未接入 · 在 Codex 对话里说：${esc(attachHint(m.name))}`;
  if (x.attached && x.last_delivery === "ok") codex += ` · 已投递 ${esc(ago(x.delivery_at))}`;
  if (x.delivery_error) codex += ` · <span class="muted">投递失败</span>`;
  return { claude, codex };
}
function nowLine(m) {
  if (m.closed) return "模块已关闭";
  if (m.pending_conclusion) return m.pending_conclusion.awaiting_confirm ? `已握手，等你确认开工（承接方 ${m.pending_conclusion.owner}）` : `已握手，承接方 ${m.pending_conclusion.owner}`;
  if (m.needs_human_q) return "等你回答";
  if (m.last_seq === 0) return "还没有信。先在下面写第一封，或让任一侧开题。";
  const from = { claude: "Claude", codex: "Codex", hou: "你" }[m.last_from] || m.last_from;
  let head = "空闲";
  if (m.waiting_for === "user") head = "等你";
  else if (sideLabel[m.waiting_for]) {
    const who = sideLabel[m.waiting_for];
    head = sideConnected(m, m.waiting_for) ? `等 ${who} 回信` : `等 ${who} 回信，但 ${who} 侧还没接入`;
  }
  return `${head} · 上封信来自 ${from} · ${ago(m.last_at)}`;
}
function renderNow() {
  const m = current;
  $("now-line").textContent = nowLine(m);
  $("now-round").textContent = `第 ${m.round}/${m.round_cap} 回合 · ${m.mode === "autopilot" ? "甩手" : "监督"}`;
  // 只有被等的那侧真接上了才算"进行中"，否则进度条是在说谎
  const busy = !m.closed && !m.needs_human_q && !m.pending_conclusion && sideConnected(m, m.waiting_for);
  $("progress").hidden = !busy;
  const t = sideText(m);
  $("side-claude").innerHTML = t.claude;
  $("side-codex").innerHTML = t.codex;
  document.querySelector('[data-copy-side="claude"]').hidden = m.claude.waiting;
  document.querySelector('[data-copy-side="codex"]').hidden = m.codex.attached;
  $("pick-codex").hidden = false;
  renderTodo(m);
  if (filledFor !== m.name) fillModuleSettings(m);
  $("ms-dir").textContent = m.dir;
  $("ms-close").hidden = m.closed; $("ms-reopen").hidden = !m.closed;
}
let filledFor = null; // 本模块设置表单上次按哪个模块填的：定时刷新不覆盖正在改的输入
function fillModuleSettings(m) {
  $("ms-name").value = m.name; $("ms-mode").value = m.mode; $("ms-cap").value = m.round_cap;
  filledFor = m.name;
}
function renderTodo(m) {
  const items = [];
  if (m.pending_conclusion && m.pending_conclusion.awaiting_confirm) {
    items.push(`<div class="todo-item"><div><b>确认开工</b> · 承接方 ${esc(m.pending_conclusion.owner)} · ${esc(m.pending_conclusion.summary)}</div>
      <div class="small mono">${esc(m.pending_conclusion.path)}</div><div><button class="btn" data-act="kickoff">确认开工</button></div></div>`);
  }
  if (m.needs_human_q) {
    items.push(`<div class="todo-item"><div><b>回答问题</b> · ${esc(m.needs_human_q)}</div>
      <textarea id="answer" rows="3" placeholder="你的回答会作为一封信发给两侧"></textarea>
      <div class="row gap"><label class="small">先回 <select id="answer-first"><option value="">按接话规则</option><option value="claude">Claude</option><option value="codex">Codex</option></select></label>
      <button class="btn" data-act="answer">发送并继续</button><button class="btn ghost" data-act="resume">只继续，不回答</button></div></div>`);
  }
  const hint = esc(attachHint(m.name));
  const attachButtons = (side) => `<div class="row gap"><button class="btn ghost tiny" data-copy="${hint}">复制「${hint}」</button>${side === "codex" ? `<button class="btn ghost tiny" data-act="pick">从列表选</button>` : ""}</div>`;
  const bothItem = !m.closed && !m.claude.waiting && !m.codex.attached && m.last_seq === 0;
  let codexCovered = false;
  if (m.codex.delivery_error) {
    if (m.codex.attached) {
      items.push(`<div class="todo-item"><div><b>Codex 没收到信</b> · ${esc(m.codex.delivery_error)}</div><div><button class="btn" data-act="redeliver">重新投递</button></div></div>`);
    } else if (!bothItem) {
      // 没接入时重新投递没用：直接给接入办法
      items.push(`<div class="todo-item"><div><b>Codex 没收到信</b> · ${esc(m.codex.delivery_error)}</div>${attachButtons("codex")}</div>`);
      codexCovered = true;
    }
  }
  const ws = m.waiting_for;
  if (!m.closed && !bothItem && sideLabel[ws] && !sideConnected(m, ws) && !(ws === "codex" && codexCovered)) {
    items.push(`<div class="todo-item"><div><b>接入 ${sideLabel[ws]} 对话</b> · 正在等 ${sideLabel[ws]} 回信，在 ${sideLabel[ws]} 对话里说一句：</div>${attachButtons(ws)}</div>`);
  }
  if (m.rejected && m.rejected.length) {
    items.push(`<div class="todo-item"><div><b>有信没法读</b> · outbox 里：${esc(m.rejected.join("、"))}（同名 .txt 里有原因）</div></div>`);
  }
  if (bothItem) {
    items.push(`<div class="todo-item"><div><b>接入两侧对话</b> · 在各自的对话里说一句：</div>
      <div class="row gap"><button class="btn ghost tiny" data-copy="${esc(attachHint(m.name))}">复制「${esc(attachHint(m.name))}」</button></div></div>`);
  }
  $("todo").hidden = items.length === 0;
  const html = items.join("");
  if ($("todo-body").dataset.html !== html) { $("todo-body").innerHTML = html; $("todo-body").dataset.html = html; }
}
async function act(name) {
  const n = encodeURIComponent(current.name);
  try {
    if (name === "pick") return pickCodex();
    if (name === "kickoff") await api(`/api/channels/${n}/auto/kickoff`, { method: "POST" });
    if (name === "resume") await api(`/api/channels/${n}/auto/resume`, { method: "POST" });
    if (name === "redeliver") await api(`/api/local/modules/${n}/redeliver`, { method: "POST" });
    if (name === "answer") {
      const text = $("answer").value.trim();
      if (!text) return toast("先写点什么");
      const first = $("answer-first").value;
      await sendLetter(text, first);
      await api(`/api/channels/${n}/auto/resume`, { method: "POST" });
    }
    await loadModules();
  } catch (e) { toast(e.message); }
}
async function sendLetter(text, first) {
  const n = encodeURIComponent(current.name);
  const body = (first ? `@${first} 先回\n\n` : "") + text;
  const summary = text.split("\n").find((l) => l.trim()) || "（无摘要）";
  await api(`/api/channels/${n}/messages`, { method: "POST", body: JSON.stringify({ to: ["claude", "codex"], summary: summary.replace(/^#+\s*/, "").slice(0, 80), body_md: body }) });
}

// ---------- 往来 ----------
const kindLabel = { resolved: "收敛提议", conclusion: "结论", kickoff: "开工", "needs-human": "需要你" };
async function loadMessages() {
  const name = current.name;
  const list = await api(`/api/channels/${encodeURIComponent(name)}/messages`);
  if (!current || current.name !== name) return; // 过期的响应
  messages = list;
  renderTimeline();
}
function renderTimeline() {
  const tl = $("timeline");
  tl.innerHTML = "";
  $("letters-count").textContent = messages.length ? `${messages.length} 封` : "";
  for (const m of messages) {
    const el = document.createElement("div");
    el.className = "letter from-" + m.from;
    const label = m.kind === "resolved" && m.ack_of ? "附和" : (kindLabel[m.kind] || "");
    const when = new Date(m.created_at).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
    el.innerHTML = `<div class="head"><span class="mono">${m.seq ? String(m.seq).padStart(3, "0") : "—"}</span><span class="eyebrow">${esc(m.from)}</span><span class="mono muted">${esc(when)}</span>${label ? `<span class="chip">${esc(label)}</span>` : ""}${m.owner ? `<span class="mono muted">承接方 ${esc(m.owner)}</span>` : ""}</div>
      <div class="summary">${esc(m.summary)}</div><div class="body" hidden></div>`;
    el.querySelector(".summary").onclick = async () => {
      const b = el.querySelector(".body");
      try {
        if (b.hidden && !b.innerHTML) { const full = await api(`/api/messages/${m.id}`); b.innerHTML = md(full.body_md); }
        b.hidden = !b.hidden;
      } catch (e) { toast(e.message); }
    };
    tl.appendChild(el);
  }
  tl.lastElementChild && tl.lastElementChild.scrollIntoView({ block: "nearest" });
}
function openSSE() {
  if (sse) sse.close();
  sse = new EventSource("/api/events?channel=" + encodeURIComponent(current.name));
  sse.addEventListener("message", (ev) => {
    const m = JSON.parse(ev.data);
    if (!messages.some((x) => x.id === m.id)) { messages.push(m); renderTimeline(); }
    loadModules().catch((e) => toast(e.message));
  });
}

// ---------- Codex 对话点选 ----------
async function pickCodex() {
  const box = $("codex-pick");
  if (!box.hidden) { box.hidden = true; return; }
  box.hidden = false;
  box.innerHTML = `<div class="muted small">读取中…</div>`;
  try {
    const convs = await api(`/api/local/conversations?side=codex&dir=${encodeURIComponent(current.dir)}`);
    box.innerHTML = convs.length ? "" : `<div class="muted small">${esc(convs.__warn || "这个目录下没有 Codex 对话；先在 Codex 里打开项目建一个对话。")}</div>`;
    for (const c of convs) {
      const b = document.createElement("button");
      b.className = "btn ghost tiny";
      b.textContent = `${c.name || c.title || c.id} · ${ago(c.updated_at)}`;
      b.onclick = async () => {
        try { await api(`/api/local/modules/${encodeURIComponent(current.name)}/attach`, { method: "POST", body: JSON.stringify({ side: "codex", thread: c.id }) }); box.hidden = true; await loadModules(); }
        catch (e) { toast(e.message); }
      };
      box.appendChild(b);
    }
  } catch (e) { box.innerHTML = `<div class="error">${esc(e.message)}</div>`; }
}

// ---------- 本模块设置 ----------
async function saveModule() {
  const patch = {};
  if ($("ms-name").value.trim() !== current.name) patch.name = $("ms-name").value.trim();
  if ($("ms-mode").value !== current.mode) patch.mode = $("ms-mode").value;
  if (Number($("ms-cap").value) !== current.round_cap) patch.round_cap = Number($("ms-cap").value);
  if (!Object.keys(patch).length) return;
  try {
    const m = await api(`/api/local/modules/${encodeURIComponent(current.name)}`, { method: "PATCH", body: JSON.stringify(patch) });
    current = m; await loadModules(); await select(m.name);
  } catch (e) { toast(e.message); }
}
async function closeModule(reopen) {
  try { await api(`/api/local/modules/${encodeURIComponent(current.name)}/${reopen ? "reopen" : "close"}`, { method: "POST" }); await loadModules(); }
  catch (e) { toast(e.message); }
}
async function deleteModule() {
  if (!confirm(`删除模块「${current.name}」的记录？项目里的信件文件默认保留。`)) return;
  const typed = prompt("要连信件文件（relais/mail/" + current.name + "）一起删吗？输入模块名确认；留空或取消 = 只删记录、保留文件");
  const files = typed !== null && typed.trim() === current.name;
  try {
    await api(`/api/local/modules/${encodeURIComponent(current.name)}${files ? "?files=1" : ""}`, { method: "DELETE" });
    current = null; if (sse) sse.close(); await loadModules();
  } catch (e) { toast(e.message); }
}

// ---------- 新建模块 ----------
async function openNew() {
  $("nm-error").textContent = "";
  $("nm-name").value = ""; $("nm-dir").value = ""; $("nm-dir-wrap").hidden = true;
  let repos = [];
  try { repos = await api("/api/local/repos"); } catch (e) { $("nm-error").textContent = "读不到项目列表：" + e.message; }
  const sel = $("nm-repo");
  sel.innerHTML = repos.map((r) => `<option value="${esc(r.dir)}">${esc(r.name)} · ${esc(r.dir)}</option>`).join("") + `<option value="__manual">手填路径…</option>`;
  $("nm-dir-wrap").hidden = sel.value !== "__manual";
  $("new-module").showModal();
  loadCodexOptions(); // 读 Codex 对话可能要一会儿，先把对话框打开
}
let codexOptsSeq = 0; // 连续换目录时只认最后一次的结果
async function loadCodexOptions() {
  const dir = $("nm-repo").value === "__manual" ? $("nm-dir").value.trim() : $("nm-repo").value;
  const sel = $("nm-codex");
  const seq = ++codexOptsSeq;
  sel.innerHTML = `<option value="">稍后再说</option>`;
  if (!dir) return;
  try {
    const convs = await api(`/api/local/conversations?side=codex&dir=${encodeURIComponent(dir)}`);
    if (seq !== codexOptsSeq) return;
    for (const c of convs) sel.innerHTML += `<option value="${esc(c.id)}">${esc(c.name || c.title || c.id)} · ${esc(ago(c.updated_at))}</option>`;
  } catch { /* 没有也行 */ }
}
async function createModule() {
  const dir = $("nm-repo").value === "__manual" ? $("nm-dir").value.trim() : $("nm-repo").value;
  try {
    const m = await api("/api/local/modules", { method: "POST", body: JSON.stringify({ name: $("nm-name").value.trim(), dir, codex_thread: $("nm-codex").value }) });
    $("new-module").close(); await loadModules(); await select(m.name);
  } catch (e) { $("nm-error").textContent = e.message; }
}

// ---------- 设置 ----------
async function openSettings() {
  try { settings = await api("/api/local/settings"); } catch (e) { return toast(e.message); }
  $("st-codex").value = settings.codex_path || ""; $("st-codex-ok").textContent = settings.codex_ok ? "可用 ✓" : "找不到或不可执行";
  $("st-mode").value = settings.default_mode; $("st-cap").value = settings.default_cap; $("st-notify").checked = !!settings.notify_every_letter;
  $("st-error").textContent = "";
  $("settings").showModal();
}
async function saveSettings() {
  try {
    await api("/api/local/settings", { method: "PUT", body: JSON.stringify({ codex_path: $("st-codex").value.trim(), default_mode: $("st-mode").value, default_cap: Number($("st-cap").value), notify_every_letter: $("st-notify").checked }) });
    $("settings").close(); loadState();
  } catch (e) { $("st-error").textContent = e.message; }
}

// ---------- 绑定 ----------
document.addEventListener("click", (ev) => {
  const t = ev.target.closest("[data-act],[data-copy],[data-copy-side]");
  if (!t) return;
  if (t.dataset.act) act(t.dataset.act);
  if (t.dataset.copy) copy(t.dataset.copy);
  if (t.dataset.copySide) copy(attachHint(current.name));
});
$("pick-codex").onclick = pickCodex;
$("composer").onsubmit = async (ev) => {
  ev.preventDefault();
  const text = $("compose-body").value.trim();
  if (!text) return;
  try { await sendLetter(text, $("compose-first").value); $("compose-body").value = ""; await loadModules(); } catch (e) { toast(e.message); }
};
$("ms-save").onclick = saveModule;
$("ms-close").onclick = () => closeModule(false);
$("ms-reopen").onclick = () => closeModule(true);
$("ms-delete").onclick = deleteModule;
$("open-new").onclick = openNew; $("empty-new").onclick = openNew;
$("nm-repo").onchange = () => { $("nm-dir-wrap").hidden = $("nm-repo").value !== "__manual"; loadCodexOptions(); };
$("nm-dir").onchange = loadCodexOptions;
$("new-form").onsubmit = (ev) => { if (ev.submitter && ev.submitter.value === "ok") { ev.preventDefault(); createModule(); } };
$("open-settings").onclick = openSettings;
$("settings-form").onsubmit = (ev) => { if (ev.submitter && ev.submitter.value === "ok") { ev.preventDefault(); saveSettings(); } };
$("st-test").onclick = async () => {
  try { await api("/api/local/settings", { method: "PUT", body: JSON.stringify({ codex_path: $("st-codex").value.trim(), default_mode: $("st-mode").value, default_cap: Number($("st-cap").value), notify_every_letter: $("st-notify").checked }) }); const s = await api("/api/local/settings"); $("st-codex-ok").textContent = s.codex_ok ? "可用 ✓" : "找不到或不可执行"; }
  catch (e) { $("st-codex-ok").textContent = e.message; }
};

(async function init() {
  await loadState();
  try { await loadModules(); } catch (e) { toast(e.message); }
  setInterval(loadState, 15000);
  setInterval(() => loadModules().catch(() => {}), 5000);
})();
