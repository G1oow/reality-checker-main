#!/usr/bin/env bash
# Reality 目标检测 · Linux 一键部署脚本
# 用法: bash install.sh [安装目录]      默认目录: /root/rc-new
set -euo pipefail

REPO="G1oow/reality-checker-main"
REF="${REF:-main}"
DIR="${1:-/root/rc-new}"
BASE="https://raw.githubusercontent.com/${REPO}/${REF}"
DATA_FILES="Country.mmdb gfwlist.conf cdn_keywords.txt hot_websites.txt exclude_rules.txt"

command -v curl >/dev/null 2>&1 || { echo "缺少 curl，请先安装: apt install -y curl"; exit 1; }

mkdir -p "${DIR}/data"
cd "${DIR}"

echo "[*] 下载主程序 -> ${DIR}/reality-checker-main"
curl -fL --retry 3 --connect-timeout 15 -o reality-checker-main "${BASE}/reality-checker-main"
chmod +x reality-checker-main

echo "[*] 下载配置与数据文件"
curl -fsSL --retry 3 -o config.yaml "${BASE}/config.yaml"
for f in ${DATA_FILES}; do
  curl -fsSL --retry 3 -o "data/${f}" "${BASE}/data/${f}"
done

echo
echo "[*] 部署完成: ${DIR}"
echo "[*] 一键运行: cd ${DIR} && ./reality-checker-main auto <你的VPS_IP> --limit 5"
echo "[*] 完整说明见: ${BASE}/Reality%E7%9B%AE%E6%A0%87%E6%A3%80%E6%B5%8B-%E5%B0%8F%E7%99%BD%E6%95%99%E7%A8%8B.md"
