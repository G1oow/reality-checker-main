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
curl -fL --retry 3 --connect-timeout 15 --max-time 900 -o reality-checker-main "${BASE}/reality-checker-main"
chmod +x reality-checker-main

echo "[*] 下载配置与数据文件"
curl -fsSL --retry 3 --connect-timeout 10 --max-time 300 -o config.yaml "${BASE}/config.yaml" || echo "  ⚠️ 下载失败: config.yaml（可稍后重跑本脚本补齐）"
for f in ${DATA_FILES}; do
  curl -fsSL --retry 3 --connect-timeout 10 --max-time 300 -o "data/${f}" "${BASE}/data/${f}" || echo "  ⚠️ 下载失败: data/${f}（可稍后重跑本脚本补齐）"
done

# ---------- 探测本机出口 IP，生成可执行的运行命令 ----------

probe_ip() { # $1 = -4 / -6
  local flag="$1" out url
  for url in "https://api${flag#-}.ipify.org" "https://ipv${flag#-}.icanhazip.com"; do
    out=$(curl "${flag}" -fsS --max-time 6 "${url}" 2>/dev/null | tr -d '[:space:]') || continue
    if [ -n "${out}" ]; then
      printf '%s' "${out}"
      return 0
    fi
  done
  return 1
}

# 优先读终端；无 tty（全自动场景）时直接返回默认值
ask() {
  local ans=""
  read -r ans 2>/dev/null < /dev/tty || true
  printf '%s' "${ans:-$1}"
}

echo
echo "[*] 部署完成: ${DIR}"
echo "[*] 正在探测本机出口 IP ..."
IP4=$(probe_ip -4 || true)
IP6=$(probe_ip -6 || true)

TARGET=""
FAMILY_FLAG=""
if [ -n "${IP4}" ] && [ -n "${IP6}" ]; then
  echo "    检测到本机双栈出口："
  echo "      1) IPv4  ${IP4}"
  echo "      2) IPv6  ${IP6}"
  printf '    用哪个作为入口 IP？[1/2]（默认 1）: '
  case "$(ask 1)" in
    2) TARGET="${IP6}"; FAMILY_FLAG="-6" ;;
    *) TARGET="${IP4}" ;;
  esac
elif [ -n "${IP4}" ]; then
  TARGET="${IP4}"
  echo "    检测到 IPv4: ${TARGET}"
elif [ -n "${IP6}" ]; then
  TARGET="${IP6}"; FAMILY_FLAG="-6"
  echo "    检测到 IPv6: ${TARGET}"
else
  echo "    ⚠️ 未能自动探测出口 IP，请手动填写"
  printf '    入口 IP: '
  TARGET=$(ask '')
fi

echo
if [ -n "${TARGET}" ]; then
  RUN_ARGS=(auto "${TARGET}" --limit 5)
  if [ -n "${FAMILY_FLAG}" ]; then
    RUN_ARGS+=("${FAMILY_FLAG}")
  fi
  echo "[*] 运行命令: cd ${DIR} && ./reality-checker-main ${RUN_ARGS[*]}"
  printf '    现在开始扫描？[y/N]: '
  case "$(ask n)" in
    y|Y|yes|YES) cd "${DIR}" && exec ./reality-checker-main "${RUN_ARGS[@]}" ;;
  esac
else
  echo "[*] 用法: cd ${DIR} && ./reality-checker-main auto <你的VPS_IP> --limit 5"
fi
echo "[*] 完整说明见: ${BASE}/Reality%E7%9B%AE%E6%A0%87%E6%A3%80%E6%B5%8B-%E5%B0%8F%E7%99%BD%E6%95%99%E7%A8%8B.md"
