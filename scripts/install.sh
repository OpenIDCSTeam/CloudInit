#!/bin/sh
# CloudInit 一键安装脚本（Linux / macOS）
#
# 用法：
#   curl -fsSL https://eo-idc.pika.net.cn/cloudinit/install.sh | sudo sh
#   curl -fsSL https://eo-idc.pika.net.cn/cloudinit/install.sh | sudo sh -s v0.4.6
#
# 环境变量：
#   INSTALL_DIR   安装目录，默认 /opt/cloudinit
#   MIRROR_BASE   镜像地址，默认 https://eo-idc.pika.net.cn/cloudinit
#   GITHUB_REPO   GitHub 仓库，默认 OpenIDCSTeam/CloudInit
set -eu

VERSION="${1:-latest}"
INSTALL_DIR="${INSTALL_DIR:-/opt/cloudinit}"
MIRROR_BASE="${MIRROR_BASE:-https://eo-idc.pika.net.cn/cloudinit}"
GITHUB_REPO="${GITHUB_REPO:-OpenIDCSTeam/CloudInit}"
SERVICE_NAME="cloudinit"
BINARY_NAME="CloudInit"

log() { printf '[CloudInit] %s\n' "$*"; }
fail() { printf '[CloudInit] 错误: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = "0" ] || fail "需要 root 权限，请使用: curl -fsSL ... | sudo sh"

OS="$(uname -s)"
case "$OS" in
  Linux)  ASSET_OS="linux" ;;
  Darwin) ASSET_OS="macos" ;;
  *) fail "不支持的系统: $OS" ;;
esac

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)  ASSET_ARCH="x64" ;;
  aarch64|arm64) ASSET_ARCH="a64" ;;
  *) fail "不支持的架构: $ARCH" ;;
esac

ASSET="CloudInit-${ASSET_OS}-${ASSET_ARCH}"
GITHUB_BASE="https://github.com/${GITHUB_REPO}"

download() {
  if command -v curl >/dev/null 2>&1; then
    curl -fL --retry 3 --retry-delay 2 --connect-timeout 15 -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$2" "$1"
  else
    fail "缺少 curl 或 wget"
  fi
}

log "系统: $OS ($ARCH)，目标: $INSTALL_DIR"

mkdir -p "$INSTALL_DIR"
TMP_FILE="$INSTALL_DIR/.CloudInit.download"
rm -f "$TMP_FILE"

if [ "$VERSION" = "latest" ]; then
  log "尝试镜像下载: $MIRROR_BASE/$ASSET"
  if ! download "$MIRROR_BASE/$ASSET" "$TMP_FILE" 2>/dev/null; then
    log "镜像不可用，回退 GitHub"
    download "$GITHUB_BASE/releases/latest/download/$ASSET" "$TMP_FILE"
  fi
else
  log "下载指定版本: $VERSION"
  download "$GITHUB_BASE/releases/download/$VERSION/$ASSET" "$TMP_FILE"
fi

[ -s "$TMP_FILE" ] || fail "下载失败"
chmod 755 "$TMP_FILE"

# 停止旧服务，避免替换正在运行的二进制
if [ "$ASSET_OS" = "linux" ]; then
  if command -v systemctl >/dev/null 2>&1; then
    systemctl stop "$SERVICE_NAME" 2>/dev/null || true
  fi
else
  launchctl bootout "system/$SERVICE_NAME" 2>/dev/null || true
  launchctl unload -w "/Library/LaunchDaemons/$SERVICE_NAME.plist" 2>/dev/null || true
fi

mv -f "$TMP_FILE" "$INSTALL_DIR/$BINARY_NAME"
chmod 755 "$INSTALL_DIR/$BINARY_NAME"
mkdir -p "$INSTALL_DIR/logs"

if [ "$ASSET_OS" = "linux" ]; then
  cat > "/etc/systemd/system/$SERVICE_NAME.service" <<EOF
[Unit]
Description=CloudInit Service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/$BINARY_NAME
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable "$SERVICE_NAME" >/dev/null 2>&1 || true
  systemctl restart "$SERVICE_NAME"
  log "已安装并启动 systemd 服务: $SERVICE_NAME"
  systemctl --no-pager --lines=0 status "$SERVICE_NAME" || true
else
  PLIST="/Library/LaunchDaemons/$SERVICE_NAME.plist"
  cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$SERVICE_NAME</string>
  <key>ProgramArguments</key><array><string>$INSTALL_DIR/$BINARY_NAME</string></array>
  <key>WorkingDirectory</key><string>$INSTALL_DIR</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$INSTALL_DIR/logs/stdout.log</string>
  <key>StandardErrorPath</key><string>$INSTALL_DIR/logs/stderr.log</string>
</dict>
</plist>
EOF

  chmod 644 "$PLIST"
  launchctl bootstrap system "$PLIST" 2>/dev/null || launchctl load -w "$PLIST"
  log "已安装并启动 launchd 服务: $SERVICE_NAME"
fi

log "安装完成，日志目录: $INSTALL_DIR/logs"
