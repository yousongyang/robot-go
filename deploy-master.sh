#!/usr/bin/env bash
# robot-master 一键部署脚本
# 用法: curl -fsSL <raw_url>/deploy-master.sh | bash
#   或: bash deploy-master.sh [选项]
#
# 选项:
#   -v, --version <tag>   指定版本（默认 latest）
#   -d, --dir <path>      安装目录（默认 ./robot-master）
#   -r, --repo <owner/repo> GitHub 仓库（默认 atframework/robot-go）

set -euo pipefail

REPO="atframework/robot-go"
VERSION="latest"
INSTALL_DIR="./robot-master"

# ---- 参数解析 ----
while [[ $# -gt 0 ]]; do
  case "$1" in
    -v|--version) VERSION="$2"; shift 2;;
    -d|--dir)     INSTALL_DIR="$2"; shift 2;;
    -r|--repo)    REPO="$2"; shift 2;;
    -h|--help)
      echo "用法: bash deploy-master.sh [-v version] [-d install_dir] [-r owner/repo]"
      exit 0;;
    *) echo "未知参数: $1"; exit 1;;
  esac
done

# ---- 检测平台 ----
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64";;
  aarch64|arm64) ARCH="arm64";;
  *) echo "不支持的架构: $ARCH"; exit 1;;
esac

case "$OS" in
  linux|darwin) ;;
  *) echo "不支持的系统: $OS (请使用 deploy-master.ps1)"; exit 1;;
esac

BIN_NAME="robot-master-${OS}-${ARCH}"

# ---- 下载地址 ----
if [[ "$VERSION" == "latest" ]]; then
  BASE_URL="https://github.com/${REPO}/releases/latest/download"
else
  BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
fi

echo "========================================"
echo "  robot-master 一键部署"
echo "========================================"
echo "  仓库:     ${REPO}"
echo "  版本:     ${VERSION}"
echo "  平台:     ${OS}-${ARCH}"
echo "  安装目录: ${INSTALL_DIR}"
echo "========================================"

mkdir -p "${INSTALL_DIR}"

# ---- 下载二进制 ----
echo "[1/3] 下载 ${BIN_NAME} ..."
curl -fSL "${BASE_URL}/${BIN_NAME}" -o "${INSTALL_DIR}/robot-master"
chmod +x "${INSTALL_DIR}/robot-master"
echo "      -> ${INSTALL_DIR}/robot-master"

# ---- 下载默认配置 ----
echo "[2/3] 下载默认配置 master.yaml ..."
if [[ -f "${INSTALL_DIR}/master.yaml" ]]; then
  echo "      -> 已存在，跳过（不覆盖已有配置）"
else
  curl -fSL "${BASE_URL}/master.yaml" -o "${INSTALL_DIR}/master.yaml"
  echo "      -> ${INSTALL_DIR}/master.yaml"
fi

# ---- 完成 ----
echo "[3/3] 部署完成！"
echo ""
echo "启动方式："
echo "  cd ${INSTALL_DIR}"
echo "  ./robot-master -config master.yaml"
echo ""
echo "或直接指定参数："
echo "  ./robot-master -listen :8080 -redis-addr localhost:6379"
echo ""
echo "浏览器打开 http://localhost:8080 访问 Web 控制台"
