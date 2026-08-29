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
    scp dist/relais-darwin-arm64 dist/relais-darwin-amd64 dist/relais-windows-amd64.exe \
        "$2:/var/lib/relais/downloads/" || echo "（downloads 目录不存在则先在服务器 mkdir -p /var/lib/relais/downloads）"
    ssh "$2" 'sudo install -m755 /tmp/relais-new /usr/local/bin/relais && sudo systemctl restart relais && systemctl is-active relais'
    echo "部署完成"
    ;;
  *) echo "用法: deploy/deploy.sh build | ship user@server"; exit 1 ;;
esac
