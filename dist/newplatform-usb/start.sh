#!/usr/bin/env bash
# NewPlatform Demo — 一键启动
# 把整个目录拷贝到 U 盘后，在终端执行:  ./start.sh
set -euo pipefail
cd "$(dirname "$0")"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  ARCH=amd64 ;;
  aarch64) ARCH=arm64 ;;
  arm64)   ARCH=arm64 ;;
esac

BIN="bin/newplatform-${OS}-${ARCH}"
if [[ ! -x "$BIN" ]]; then
  # 尝试不带平台后缀的二进制
  for f in bin/newplatform-*; do
    if [[ -x "$f" && ! "$f" == *.exe ]]; then
      BIN="$f"
      echo "⚠ 未找到精确匹配的二进制，使用 $BIN"
      break
    fi
  done
fi

if [[ ! -x "$BIN" ]]; then
  echo "❌ 未找到可执行二进制。请确认 bin/ 目录下有对应平台的文件。"
  exit 1
fi

echo ""
echo "╔══════════════════════════════════════════════════╗"
echo "║        NewPlatform Demo  —  离线模式             ║"
echo "╚══════════════════════════════════════════════════╝"
echo ""
echo "  打开浏览器访问: http://127.0.0.1:8080"
echo "  按 Ctrl+C 停止"
echo ""

exec "$BIN"
