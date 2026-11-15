#!/usr/bin/env bash
# Reality 目标检测 · Linux 一键部署脚本
# 用法: bash install.sh [安装目录]      默认目录: /root/rc-new
#
# 装完在交互终端内会直接进入 VPS 终端面板；非交互环境（curl | bash、CI）只打印后续用法。
# 可用环境变量：
#   REF / REF_TAG   规则库分支与主程序 release 版本（默认 main / latest）
#   RC_BASE         规则库下载基址覆盖（默认 GitHub raw）
#   RC_REL_BASE     主程序下载基址覆盖（默认 GitHub Releases）
#   RC_BIN_DIR      快捷指令安装目录（默认 /usr/local/bin）
#   RC_NO_PANEL     置 1 时安装完成后不进入面板
set -euo pipefail

REPO="G1oow/reality-checker-main"
REF="${REF:-main}"
REF_TAG="${REF_TAG:-latest}"
DIR="${1:-/root/rc-new}"
BASE="${RC_BASE:-https://raw.githubusercontent.com/${REPO}/${REF}}"
if [[ "${REF_TAG}" = "latest" ]]; then
  REL_BASE="${RC_REL_BASE:-https://github.com/${REPO}/releases/latest/download}"
else
  REL_BASE="${RC_REL_BASE:-https://github.com/${REPO}/releases/download/${REF_TAG}}"
fi
BIN_DIR="${RC_BIN_DIR:-/usr/local/bin}"
APP="reality-checker-main"
DATA_FILES="Country.mmdb gfwlist.conf cdn_keywords.txt hot_websites.txt exclude_rules.txt"

step() { printf '› %s\n' "$1"; }
ok()   { printf '✔ %s\n' "$1"; }
warn() { printf '! %s\n' "$1" >&2; }
fail() { printf '✖ %s\n' "$1" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || fail "缺少 curl，请先安装：apt install -y curl"

printf '› 安装目录：%s\n' "${DIR}"
mkdir -p "${DIR}/data"
cd "${DIR}"

printf '› 下载主程序：release %s\n' "${REF_TAG}"
curl -fL --retry 3 --connect-timeout 15 --max-time 900 -o "${APP}" "${REL_BASE}/${APP}" \
  || fail "主程序下载失败：${REL_BASE}/${APP}（确认仓库已发布 release，或用 REF_TAG=<tag> 指定版本）"
chmod +x "${APP}"
ok "主程序就绪：${DIR}/${APP}"

printf '› 下载配置与离线规则库\n'
curl -fsSL --retry 3 --connect-timeout 10 --max-time 300 -o config.yaml "${BASE}/config.yaml" \
  || warn "config.yaml 下载失败，可重跑本脚本补齐"
for f in ${DATA_FILES}; do
  curl -fsSL --retry 3 --connect-timeout 10 --max-time 300 -o "data/${f}" "${BASE}/data/${f}" \
    || warn "data/${f} 下载失败，可重跑本脚本补齐"
done
ok "配置与规则库就绪：${DIR}/config.yaml、${DIR}/data/"

printf '› 安装快捷指令：%s/rcm\n' "${BIN_DIR}"
if [[ -d "${BIN_DIR}" && ( -w "${BIN_DIR}" || "$(id -u)" = "0" ) ]]; then
  mkdir -p "${BIN_DIR}"
  if ! curl -fsSL --connect-timeout 10 --max-time 60 -o "${BIN_DIR}/rcm" "${BASE}/rcm"; then
    cat > "${BIN_DIR}/rcm" <<'RCM_FALLBACK'
#!/usr/bin/env bash
# R-C-M 面板快捷启动器（install.sh 内置兜底版本）
set -euo pipefail
APP_NAME="reality-checker-main"
if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  echo "用法: rcm [面板选项]    rcm 进入面板，rcm --help 查看选项，rcm --no-tui 纯文本扫描"
  exit 0
fi
resolve_dir() {
  local candidate
  if [[ -n "${RC_HOME:-}" && -x "${RC_HOME}/${APP_NAME}" ]]; then printf '%s' "${RC_HOME}"; return 0; fi
  local self_dir
  self_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  if [[ -x "${self_dir}/${APP_NAME}" ]]; then printf '%s' "${self_dir}"; return 0; fi
  for candidate in "/root/rc-new" "${HOME:-/root}/rc-new" "/opt/rc" "/usr/local/rc"; do
    if [[ -x "${candidate}/${APP_NAME}" ]]; then printf '%s' "${candidate}"; return 0; fi
  done
  return 1
}
if ! DIR="$(resolve_dir)"; then
  printf '✖ 未找到 %s：请先运行 install.sh 安装，或用 RC_HOME=<安装目录> rcm 指定位置\n' "${APP_NAME}" >&2
  exit 1
fi
cd "${DIR}"
exec "./${APP_NAME}" ui "$@"
RCM_FALLBACK
  fi
  chmod +x "${BIN_DIR}/rcm"
  ok "已安装：${BIN_DIR}/rcm"
  # 同时在安装目录留一份，便于直接 ./rcm 或脚本同级调用
  cp -f "${BIN_DIR}/rcm" "${DIR}/rcm" && chmod +x "${DIR}/rcm"
else
  warn "无 ${BIN_DIR} 写权限，跳过 rcm 安装（可手动复制仓库中的 rcm 到 PATH）"
fi

# 交互终端下直接进入面板；非交互环境只打印用法，绝不阻塞
if [[ -z "${RC_NO_PANEL:-}" && -t 0 && -t 1 ]]; then
  printf '\n✔ 安装完成：%s\n' "${DIR}"
  printf '› 正在进入面板（首次进入会自动探测本机出口 IPv4/IPv6）\n\n'
  cd "${DIR}"
  exec "./${APP}" ui
fi

cat <<EOF

✔ 安装完成：${DIR}
› 进入面板：rcm
› 直接扫描（无终端环境）：cd ${DIR} && ./${APP} ui --no-tui --mode country --country DE --limit 3
› 完整说明：${BASE}/README.md
EOF
