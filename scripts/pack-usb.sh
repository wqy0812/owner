#!/usr/bin/env bash
# pack-usb.sh — 把 NewPlatform Demo 打包成可离线运行的 USB 目录。
#
# 用法:
#   ./scripts/pack-usb.sh                  # 仅打包 macOS arm64（当前机器）
#   ./scripts/pack-usb.sh --cross          # 同时交叉编译 macOS amd64、Linux amd64/arm64、Windows amd64
#
# 输出:
#   dist/newplatform-usb/        — 可以直接拷贝到 U 盘的目录
#   dist/newplatform-usb.tar.gz  — 压缩归档
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$PROJECT_ROOT"

CROSS=false
if [[ "${1:-}" == "--cross" ]]; then
  CROSS=true
fi

DIST="$PROJECT_ROOT/dist/newplatform-usb"
rm -rf "$DIST"
mkdir -p "$DIST"

# ---------- 1. 确保前端已构建并嵌入 ----------
echo "==> 构建前端 & Go 二进制 …"
make build 2>&1 | tail -4

# ---------- 2. 二进制文件 ----------
mkdir -p "$DIST/bin"
cp bin/newplatform "$DIST/bin/newplatform-darwin-arm64"
cp bin/clusterforge-backup "$DIST/bin/clusterforge-backup-darwin-arm64"

if $CROSS; then
  echo "==> 交叉编译其他平台 …"
  for target in darwin/amd64 linux/amd64 linux/arm64 windows/amd64; do
    os="${target%/*}"
    arch="${target#*/}"
    ext=""
    [[ "$os" == "windows" ]] && ext=".exe"
    echo "    $os/$arch"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
      go build -tags embed -o "$DIST/bin/newplatform-${os}-${arch}${ext}" ./cmd/server
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
      go build -o "$DIST/bin/clusterforge-backup-${os}-${arch}${ext}" ./cmd/backup
  done
fi

# ---------- 3. 数据目录（种子数据库 + ansible 示例） ----------
echo "==> 复制数据与示例 …"
mkdir -p "$DIST/data"
cp data/newplatform.db "$DIST/data/newplatform.db"

cp -R examples "$DIST/examples"

# ---------- 4. 配置文件 ----------
cat > "$DIST/.env" <<'EOF'
NEWPLATFORM_ADDR=127.0.0.1:8080
NEWPLATFORM_DB_PATH=./data/newplatform.db
NEWPLATFORM_ANSIBLE_BIN=ansible-playbook
NEWPLATFORM_RUN_ROOT=./data/runs
NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS=./examples/ansible
NEWPLATFORM_KILL_GRACE=3s
NEWPLATFORM_MAX_LOG_BYTES=2097152
CLUSTERFORGE_BACKUP_ENABLED=false
# NEWPLATFORM_K8S1175_ENCRYPTION_KEY=
EOF

# ---------- 5. 启动脚本 ----------

# --- macOS / Linux ---
cat > "$DIST/start.sh" <<'LAUNCHER'
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
LAUNCHER
chmod +x "$DIST/start.sh"

# --- Windows ---
cat > "$DIST/start.bat" <<'BATFILE'
@echo off
chcp 65001 >nul 2>&1
cd /d "%~dp0"

echo.
echo ╔══════════════════════════════════════════════════╗
echo ║        NewPlatform Demo  —  离线模式             ║
echo ╚══════════════════════════════════════════════════╝
echo.
echo   打开浏览器访问: http://127.0.0.1:8080
echo   按 Ctrl+C 停止
echo.

if exist "bin\newplatform-windows-amd64.exe" (
    bin\newplatform-windows-amd64.exe
) else (
    echo 未找到 Windows 二进制。请使用 --cross 参数重新打包。
    pause
)
BATFILE

# ---------- 6. README ----------
cat > "$DIST/README.txt" <<'README'
═══════════════════════════════════════════════════
  NewPlatform Demo — 离线 USB 版
═══════════════════════════════════════════════════

■ 快速开始

  macOS / Linux:
    1. 在终端中进入此目录
    2. 执行  ./start.sh
    3. 打开浏览器访问  http://127.0.0.1:8080

  Windows:
    1. 双击  start.bat
    2. 打开浏览器访问  http://127.0.0.1:8080

  按 Ctrl+C 停止服务。

■ 目录结构

  start.sh / start.bat    — 一键启动脚本
  bin/                    — 各平台的可执行二进制
  data/newplatform.db     — SQLite 数据库（已含 Demo 种子数据）
  data/runs/              — 运行时工作区（自动创建）
  examples/ansible/       — Ansible 作业快照
  .env                    — 配置文件（可选修改）

■ 前置条件

  • 无需安装 Go、Node.js 或任何开发工具。
  • 如需真正执行 Ansible Playbook，目标机器上需要安装 ansible-playbook。
  • 仅查看 UI、管理组件/场景/环境无需任何额外依赖。

■ 配置

  编辑 .env 文件即可修改监听地址、数据库路径等参数，
  详见 .env 文件内的注释。

■ 注意

  这是本地 Demo 演示版，不是生产控制面。
  身份切换不包含密码认证。
═══════════════════════════════════════════════════
README

# ---------- 7. 打包压缩 ----------
echo "==> 生成 tar.gz 归档 …"
cd "$PROJECT_ROOT/dist"
tar czf newplatform-usb.tar.gz newplatform-usb/

echo ""
echo "✅ 打包完成！"
echo ""
echo "   目录: $DIST"
echo "   归档: $PROJECT_ROOT/dist/newplatform-usb.tar.gz"
echo ""
du -sh "$DIST" "$PROJECT_ROOT/dist/newplatform-usb.tar.gz"
echo ""
echo "把 newplatform-usb/ 目录或 .tar.gz 文件拷贝到 U 盘即可。"
