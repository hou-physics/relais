#!/usr/bin/env bash
# 用法:
#   deploy/deploy.sh build                # 只交叉编译到 dist/
#   deploy/deploy.sh ship user@server     # 编译 + 上传 + 重启服务 + 上传下载包
set -euo pipefail
cd "$(dirname "$0")/.."
build() {
  mkdir -p dist
  echo "交叉编译（CGO_ENABLED=0）..."
  GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -o dist/relais-linux-amd64 .
  GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -o dist/relais-darwin-arm64 .
  GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -o dist/relais-darwin-amd64 .
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o dist/relais-windows-amd64.exe .
  ls -lh dist/
}
case "${1:-}" in
  build) build ;;
  ship)
    [ -n "${2:-}" ] || { echo "用法: deploy/deploy.sh ship user@server"; exit 1; }
    build
    ./scripts/check.sh
    scp dist/relais-linux-amd64 "$2:/tmp/relais-new"
    # downloads/ 里要放齐 install.sh/install.ps1 会请求的四个平台二进制（含 linux，M5 install.sh 支持 Linux）
    scp dist/relais-linux-amd64 dist/relais-darwin-arm64 dist/relais-darwin-amd64 dist/relais-windows-amd64.exe \
        "$2:/var/lib/relais/downloads/" || echo "（上传 downloads 失败：请确认服务器已 mkdir -p /var/lib/relais/downloads 且当前 ssh 用户有写权限，或用 sudo 手动拷贝)"
    ssh "$2" 'sudo install -m755 /tmp/relais-new /usr/local/bin/relais && sudo systemctl restart relais && systemctl is-active relais'
    echo "部署完成"
    ;;
  *) echo "用法: deploy/deploy.sh build | ship user@server"; exit 1 ;;
esac
