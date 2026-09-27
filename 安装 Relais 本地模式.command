#!/bin/bash
# 双击安装 / 升级 Relais 本地模式（M9）。重复双击 = 升级，数据与模块不动。
set -euo pipefail
cd "$(dirname "$0")"

dialog() { osascript -e "display dialog \"$1\" buttons {\"好\"} default button 1 with title \"Relais 本地模式\"" >/dev/null 2>&1 || true; }
fail() { dialog "$1"; echo "错误: $1" >&2; exit 1; }

command -v go >/dev/null 2>&1 || fail "没找到 Go。请先安装 Go（go.dev/dl）再双击本文件。"

BIN_DIR=""
for D in /opt/homebrew/bin /usr/local/bin; do
  if [ -d "$D" ] && [ -w "$D" ]; then BIN_DIR="$D"; break; fi
done
if [ -z "$BIN_DIR" ]; then BIN_DIR="$HOME/bin"; mkdir -p "$BIN_DIR"; fi
echo "编译 relais → $BIN_DIR/relais"
CGO_ENABLED=0 go build -o "$BIN_DIR/relais" . || fail "编译失败，详情见终端。"
RELAIS="$BIN_DIR/relais"

echo "配置本地模式…"
OUT="$("$RELAIS" local bootstrap --json)" || fail "bootstrap 失败，详情见终端。"
BASE="$(printf '%s' "$OUT" | sed -n 's/.*"base_url":"\([^"]*\)".*/\1/p')"

# 升级路径：常驻已存在时重启，让新二进制生效（旧的两个 bridge 常驻由 bootstrap 卸掉）
if [ -f "$HOME/Library/LaunchAgents/com.relais.local.serve.plist" ]; then
  launchctl kickstart -k "gui/$(id -u)/com.relais.local.serve" 2>/dev/null || true
fi

WHERE="relais 已装到 $BIN_DIR"
if [ "$BIN_DIR" = "$HOME/bin" ]; then WHERE="$WHERE（请确认它在 PATH 里）"; fi
dialog "完成。控制台：$BASE\n不需要登录，直接打开就是。\n$WHERE"
open "$BASE"
