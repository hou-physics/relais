# Relais M8 设计：本地控制台（网页取代命令行）+ 双击安装

> 日期：2026-09-27 · 状态：**定稿待 Hou 审阅** · 基线：M7 本地单人模式（分支 88e43f9，待合 main）
> 决策：D43 · 术语：`CONTEXT.md`（本文只引用）· 原"M8 拷问 + 密封轮"顺延为 M9
> 来源：Hou 2026-09-27 "给我做一个图形界面，不想记 command line"，四问定案（网页即可 / 三处命令都做成按钮 / 扫仓库点选 / 双击安装）

---

## 1. 目标

M7 之后 Hou 日常还要敲四处命令：装二进制、`relais local init`、`relais send`、`relais conclusion`。M8 把它们全部收进两样东西：**一个双击安装文件**（一次性）和 **现有网页扩成的控制台**（日常）。工作脑那两处（开题、开工）由 `AGENT.md` 教给 Claude Code / Codex 自己执行，人只说话不敲命令。命令行照旧可用，但不再是必需。

**不做**：原生 macOS app；系统文件夹选择框；远程访问（控制台接口只认回环 + 人的钥匙）；多用户；改联网层（D35）。

## 2. 双击安装文件

仓库根目录放 `安装 Relais 本地模式.command`（bash，`chmod +x`，进 release 附件）。双击后按顺序：

1. `cd` 到脚本所在目录；检查 `go` 存在，否则弹系统对话框指引装 Go 并退出。
2. `CGO_ENABLED=0 go build -o /opt/homebrew/bin/relais .`；若该目录不可写则装到 `~/bin/relais` 并提示加 PATH。
3. 侦测 claude / codex：先 `command -v`，codex 再查 `~/.codex/plugins/.plugin-appserver/codex`；仍找不到的用 `osascript -e 'choose file'` 弹一次让人选，选择结果传给下一步。
4. `relais local bootstrap [--claude P] [--codex P]`（新子命令，§3.2）：只做"环境"，不建模块。
5. 用 `osascript display dialog` 显示网页地址、账号 `hou` 与初始密码（仅首次创建时；已存在则显示"密码见 human.txt"）。
6. `open http://127.0.0.1:8080`。

重复双击 = 升级：重新编译、`launchctl kickstart -k` 重启三个常驻，数据与模块不动。脚本进 `./scripts/check.sh` 的 `bash -n`。

## 3. 服务器：本地管理接口

### 3.1 启用条件
`server.toml` 新增 `local_dir`（bootstrap 写入，指向 `~/Library/Application Support/relais-local`）。非空时服务器挂载 `/api/local/*`，并把全部本地接口限定为：**人的钥匙**（agent token → 403）且 **`r.RemoteAddr` 是回环地址**（否则 403）。`local_dir` 为空（线上）时这些路由不注册。

### 3.2 共享包 `internal/local`
把 `internal/cli/local.go` 的逻辑抽成包，CLI 与服务器共用，避免两份实现：

```go
type Env struct{ Dir string; Store *store.Store; BaseURL string }   // Dir = local_dir
func Bootstrap(dir, listen, claudePath, codexPath string, service bool) (BootstrapResult, error) // server.toml、三账号、两侧配置目录+hook、launchd；不建模块
func (e Env) ScanRepos(home string, depth int) ([]Repo, error)      // 家目录 depth 层内含 .git 的目录，按修改时间倒序，最多 50
func (e Env) CreateModule(name, dir string) (Module, error)         // 频道+三成员+auto(16)+mode(默认)+两侧 projects.toml+项目 relais/{config,RULES,AGENT,conclusions}；幂等
func (e Env) ListModules() ([]Module, error)                        // 名称、目录、mode、round/cap、状态、结论数、两侧心跳
func (e Env) CloseModule(name string) error                         // CloseChannel + 两侧 sessionClear
func (e Env) Rules(name string) (string, error); func (e Env) PutRules(name, text string) error
func (e Env) Settings() (Settings, error); func (e Env) PutSettings(Settings) error  // claude_path/codex_path/default_mode，存 settings 表 local.*；改路径时重写两侧 hook
```
`relais local init/status/close` 改为薄壳调用本包；新增 `relais local bootstrap`。

### 3.3 端点
| 方法 路径 | 作用 | 钥匙 |
|---|---|---|
| `GET /api/local/repos` | `ScanRepos(home, 2)` | 人 |
| `GET /api/local/modules` | `ListModules` | 人 |
| `POST /api/local/modules` `{name, dir}` | `CreateModule`；名字校验同 CLI；目录须存在 | 人 |
| `POST /api/local/modules/{name}/close` | `CloseModule` | 人 |
| `GET/PUT /api/local/modules/{name}/rules` | 读写 `<dir>/relais/RULES.md` | 人 |
| `GET/PUT /api/local/settings` | 路径与默认模式 | 人 |
| `POST /api/local/heartbeat` | bridge 报活；侧 = 调用者用户名 | agent |

心跳存服务器内存 `map[side]time.Time`（不落库）；`ListModules` 里 `bridge_alive[side] = now - last < 20s`。

## 4. bridge 两处小改
- 每次轮询前重读 `projects.toml`（`loadProjects`），新增模块无需重启；失效目录照旧跳过。
- 每次轮询后 `POST /api/local/heartbeat`（失败静默；联网服务器无此路由 → 404 静默）。

## 5. 网页

### 5.1 「模块」页（顶栏新增入口，仅本地模式显示：`GET /api/local/modules` 200 即显示）
- 列表：名称、目录、模式、`round/round_cap`、状态（运行/暂停/等你/已握手/已开工/已关闭）、结论数、两侧 bridge 心跳（绿点/灰点 + "N 秒前"）。
- 「新建模块」：名字输入框 + 文件夹（下拉列出 `repos`，末项"手填路径"展开文本框）+ 创建按钮；成功后列表刷新并高亮新行，失败弹 `alert`（沿用 `humanAction`）。
- 每行：「打开频道」「编辑规矩」（textarea + 保存）「关闭」（confirm）。
- 「设置」：claude/codex 路径、默认模式；保存后提示"两侧 hook 已重写"。
- 没有模块时首页显示引导："先去「模块」页新建一个模块"。

### 5.2 频道页
- 「开题」框（在 composer 上方，仅本地模式、频道空闲时显示）：正文 textarea + "先由谁回应"下拉（Claude / Codex，默认上次选择，初始 Claude）+ 发出。发出 = 以本人身份 `POST messages`，`to` = 两侧，`summary` = 正文首行前 80 字，`body_md` = `@<side> 先回\n\n` + 正文。发出后时间线出现该信，状态条显示"第 1/8 回合"。
- 结论卡片新增「复制开工指令」：剪贴板内容 `读 relais/conclusions/<频道>-<结论id>.md，按结论开工`（文件名由卡片的 message id 拼出，与 bridge 落盘规则一致）。已开工状态下卡片显示该路径。
- 三语文案照加。

## 6. 接话规则扩展（D42 ⑦ 之上）
`humanMsgResponder`：若触发消息正文首行匹配 `^@(claude|codex)\b`，则该侧为接话方（仍须在收件人里）；否则沿用原规则。`local-prompt` 加一句："正文首行若以 @ 开头，是给系统的路由指示，忽略它。"

## 7. 工作脑说明（`relais/AGENT.md` 本地模式一节，由 `internal/guide` 生成）
教工作脑两件事，人只说话：
- 听到"把这个拿去讨论 / 让 Codex 看看"：把当前上下文压缩成一封信（frontmatter `summary:`），执行 `RELAIS_CONFIG_DIR="<本侧目录>" RELAIS_CHANNEL="<模块>" relais send <文件>`（本侧目录与模块名在 AGENT.md 生成时写死）。
- 听到"开工 / 按结论做"：读 `relais/conclusions/` 下本模块最新文件，按其正文实施。
Codex 侧同文（AGENTS.md 兼容）。

## 8. 安全不变量
本地接口人钥匙 + 回环双重限制（永久 e2e 锚点：agent token → 403；非回环 RemoteAddr → 403）；`CreateModule` 目录必须已存在且不含 `..`；`PutRules` 只写 `<dir>/relais/RULES.md`；`PutSettings` 路径必须是存在的普通文件；线上（无 `local_dir`）不注册任何本地路由（锚点：`/api/local/modules` → 404）。

## 9. 测试与验收
- 锚点：本地路由钥匙/回环隔离；线上 404；`CreateModule` 幂等（两次调用状态一致、projects.toml 无重复）；bridge 重读登记表后下一轮拉到新模块的消息；心跳→`bridge_alive`；`@codex 先回` 接话规则（单侧被拒文案不变）。
- 单测：`ScanRepos`（临时家目录三层，只取两层内含 .git）；`Bootstrap` 幂等（`--no-service`，假 agent 路径）；安装脚本 `bash -n`；网页新 id 与三语键；`AGENT.md` 本地段落内容。
- 真实冒烟（本机，隔离目录同 M7 裁定）：双击安装脚本以 `--no-service` 等价路径跑通 → 网页新建模块 → 开题（选 Codex 先回）→ 握手 → 复制开工指令 → 工作脑按指令读到结论。

## 10. 版本
`0.6.0-m8`；原 `2026-09-26-relais-m8-grill-design.md` 标题改为 M9、D41 注明顺延。
