#!/usr/bin/env bash
# M9 隔离冒烟：临时 RELAIS_LOCAL_DIR、--no-service、假 codex、临时项目。绝不碰 ~/Library、launchd。
set -euo pipefail
cd "$(dirname "$0")/.."
export RELAIS_LOCAL_DIR="$(mktemp -d)"
PROJ="$(mktemp -d)"
FAKE="$(mktemp -d)"
LOG="$FAKE/codex.log"
cat > "$FAKE/codex" <<EOS
#!/bin/sh
printf '%s\n' "\$@" >> "$LOG"
EOS
chmod +x "$FAKE/codex"
export CODEX_HOME="$FAKE/codexhome"; mkdir -p "$CODEX_HOME"
sqlite3 "$CODEX_HOME/state_5.sqlite" "CREATE TABLE threads (id TEXT PRIMARY KEY, cwd TEXT NOT NULL, title TEXT NOT NULL, name TEXT, archived INTEGER NOT NULL DEFAULT 0, updated_at_ms INTEGER); INSERT INTO threads VALUES ('t-1','$PROJ','冒烟对话','冒烟',0,1);"
trap 'kill $SERVE 2>/dev/null || true; rm -rf "$RELAIS_LOCAL_DIR" "$PROJ" "$FAKE"' EXIT
go build -o "$FAKE/relais" .
R="$FAKE/relais"
"$R" local bootstrap --no-service --listen 127.0.0.1:18098 --json
"$R" serve --config "$RELAIS_LOCAL_DIR/server.toml" >"$FAKE/serve.log" 2>&1 & SERVE=$!
sleep 1
curl -sf -X PUT localhost:18098/api/local/settings -d "{\"codex_path\":\"$FAKE/codex\",\"default_mode\":\"supervised\",\"default_cap\":8}"
curl -sf -X POST localhost:18098/api/local/modules -d "{\"name\":\"smoke\",\"dir\":\"$PROJ\"}" >/dev/null
cd "$PROJ"
"$R" attach smoke --as codex
curl -sf -X POST localhost:18098/api/channels/smoke/messages -d '{"to":["claude","codex"],"summary":"第一封","body_md":"@codex 先回\n\n第一封"}' >/dev/null
sleep 3
test -f relais/mail/smoke/001-hou.md || { echo "❌ 雇主信没归档"; exit 1; }
grep -q "由你先回" "$LOG" || { echo "❌ codex 没收到投递"; cat "$LOG"; exit 1; }
printf '提议：就这么办\n' > relais/mail/smoke/drafts/c.md
"$R" post smoke relais/mail/smoke/drafts/c.md --as codex --resolved --owner codex
sleep 3
"$R" wait smoke --as claude --timeout 5s | grep -q "002-codex.md" || { echo "❌ claude wait 没拿到"; exit 1; }
printf '附和\n' > relais/mail/smoke/drafts/a.md
"$R" post smoke relais/mail/smoke/drafts/a.md --as claude --ack
sleep 3
test -f relais/mail/smoke/conclusion-003.md || { echo "❌ 没有结论文件"; ls relais/mail/smoke; exit 1; }
curl -sf -X POST localhost:18098/api/channels/smoke/auto/kickoff >/dev/null
sleep 3
test -f relais/mail/smoke/kickoff-003.md || { echo "❌ 没有开工文件"; exit 1; }
grep -q "你是承接方" "$LOG" || { echo "❌ codex 没收到开工通知"; exit 1; }
curl -sf -X PATCH localhost:18098/api/local/modules/smoke -d '{"name":"smoke2"}' >/dev/null
test -d relais/mail/smoke2 || { echo "❌ 改名没移目录"; exit 1; }
curl -sf -X DELETE localhost:18098/api/local/modules/smoke2 >/dev/null
test -d relais/mail/smoke2 || { echo "❌ 删除默认不该删文件"; exit 1; }
echo "✅ M9 冒烟通过"
