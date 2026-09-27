#!/bin/bash
# 双击安装 / 升级 Relais 本地模式（spec 2026-09-27 M8 §2）。重复双击 = 升级，数据不动。
set -euo pipefail
cd "$(dirname "$0")"

dialog() { osascript -e "display dialog \"$1\" buttons {\"好\"} default button 1 with title \"Relais 本地模式\"" >/dev/null 2>&1 || true; }
fail() { dialog "$1"; echo "错误: $1" >&2; exit 1; }

command -v go >/dev/null 2>&1 || fail "没找到 Go。请先安装 Go（https://go.dev/dl/）再双击本文件。"

BIN_DIR=/opt/homebrew/bin
if [ ! -w "$BIN_DIR" ]; then BIN_DIR="$HOME/bin"; mkdir -p "$BIN_DIR"; fi
echo "编译 relais → $BIN_DIR/relais"
CGO_ENABLED=0 go build -o "$BIN_DIR/relais" .
RELAIS="$BIN_DIR/relais"

pick() { osascript -e "POSIX path of (choose file with prompt \"$1\")" 2>/dev/null || true; }
CLAUDE="$(command -v claude || true)"
[ -z "$CLAUDE" ] && CLAUDE="$(pick '请选择 claude 可执行文件')"
CODEX="$(command -v codex || true)"
[ -z "$CODEX" ] && [ -x "$HOME/.codex/plugins/.plugin-appserver/codex" ] && CODEX="$HOME/.codex/plugins/.plugin-appserver/codex"
[ -z "$CODEX" ] && CODEX="$(pick '请选择 codex 可执行文件（通常在 ~/.codex/plugins/.plugin-appserver/codex）')"
[ -n "$CLAUDE" ] && [ -n "$CODEX" ] || fail "没有选到 claude 或 codex，安装中止。"

echo "配置本地模式…"
OUT="$("$RELAIS" local bootstrap --claude "$CLAUDE" --codex "$CODEX" --json)" || fail "bootstrap 失败，详情见终端。"
BASE="$(printf '%s' "$OUT" | sed -n 's/.*"base_url":"\([^"]*\)".*/\1/p')"
# 注意：macOS 自带 BSD sed 的基本正则不认 \|，这里用 -E 扩展正则
SHOWN="$(printf '%s' "$OUT" | sed -nE 's/.*"password_shown":(true|false).*/\1/p')"
PW="$(printf '%s' "$OUT" | sed -n 's/.*"human_password":"\([^"]*\)".*/\1/p')"

# 升级路径：常驻已存在时重启三个服务，让新二进制生效
for L in com.relais.local.serve com.relais.local.bridge.claude com.relais.local.bridge.codex; do
  if [ -f "$HOME/Library/LaunchAgents/$L.plist" ]; then launchctl kickstart -k "gui/$(id -u)/$L" 2>/dev/null || true; fi
done

if [ "$SHOWN" = "true" ]; then
  dialog "安装完成。控制台：$BASE\n账号：hou\n初始密码：$PW\n（已存到 ~/Library/Application Support/relais-local/human.txt）"
else
  dialog "升级完成。控制台：$BASE\n账号：hou（密码见 ~/Library/Application Support/relais-local/human.txt）"
fi
open "$BASE"
