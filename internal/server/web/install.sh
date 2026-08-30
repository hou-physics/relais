#!/bin/sh
# Relais 一行安装（Mac/Linux）：curl -fsSL <base>/install.sh | sh
set -e
BASE="__BASE_URL__"
OS="$(uname -s)"; ARCH="$(uname -m)"
case "$OS" in
  Darwin) case "$ARCH" in arm64) F=relais-darwin-arm64;; *) F=relais-darwin-amd64;; esac;;
  Linux)  F=relais-linux-amd64;;
  *) echo "不支持的系统 $OS"; exit 1;;
esac
DEST="$HOME/.local/bin"; mkdir -p "$DEST"
echo "下载 $F ..."
curl -fsSL "$BASE/download/$F" -o "$DEST/relais"
chmod +x "$DEST/relais"
case ":$PATH:" in *":$DEST:"*) : ;; *)
  echo "export PATH=\"$DEST:\$PATH\"" >> "$HOME/.zshrc" 2>/dev/null || true
  echo "export PATH=\"$DEST:\$PATH\"" >> "$HOME/.bashrc" 2>/dev/null || true
  echo "已把 $DEST 加进 PATH（新开终端生效）";;
esac
"$DEST/relais" version && echo "安装完成。下一步：relais login ... 然后 relais setup"
