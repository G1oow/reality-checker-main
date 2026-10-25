#!/usr/bin/env bash
# vps-init.sh —— 新 VPS 初始化工作流（阶段化，可重复执行，幂等）
# 目标系统：Debian 13（minimal）。Ubuntu 23.04+ 的 ssh.socket 会自动识别并走 socket 分支兜底。
#
# 用法：
#   ./vps-init.sh 0            # SSH 加固：装公钥 + 改端口 + 防火墙放行（不改密码登录）
#   ./vps-init.sh 0b           # 确认新端口能登入后，关闭密码登录（含 keyboard-interactive）
#   ./vps-init.sh 1            # TCP 质量检测（TcpQuality）
#   ./vps-init.sh 2            # 综合跑分（NodeQuality）
#   ./vps-init.sh 3            # TCP 调优（tcpfit，交互菜单）
#   ./vps-init.sh 4            # Reality 伪装目标筛选（reality-checker）
#   ./vps-init.sh 5            # sing-box / Xray 多协议面板（singbox-lite）
#   ./vps-init.sh rollback     # 把备份的 sshd 配置写回去并重载
#
# 常用环境变量：
#   SSH_PUBKEY='ssh-ed25519 AAAA... me@pc'   公钥文本
#   SSH_PUBKEY_FILE=/root/mykey.pub          或从文件读
#   SSH_PORT=30000                           目标端口
#   DRY_RUN=1                                只打印，不执行
#
set -Eeuo pipefail

SSH_PORT="${SSH_PORT:-30000}"
SSH_PUBKEY="${SSH_PUBKEY:-}"
SSH_PUBKEY_FILE="${SSH_PUBKEY_FILE:-}"
DRY_RUN="${DRY_RUN:-0}"
BACKUP_ROOT="${BACKUP_ROOT:-/root/vps-init-backup}"

SSHD_CONFIG=/etc/ssh/sshd_config
SOCKET_DROPIN=/etc/systemd/system/ssh.socket.d/10-vps-init-port.conf
AUTH_KEYS=/root/.ssh/authorized_keys

STAMP="$(date +%Y%m%d-%H%M%S)"

# 各阶段的官方一键地址（已按上游 README 核对）
SRC_TCPQUALITY='https://raw.githubusercontent.com/ibsgss/TcpQuality/main/runTcpQuality.sh'
SRC_NQ='https://run.NodeQuality.com'
SRC_TCPFIT='https://raw.githubusercontent.com/Kylin010/tcpfit/main/tcpfit.sh'
SRC_RC_INSTALL='https://raw.githubusercontent.com/G1oow/reality-checker-main/main/install.sh'
SRC_SB='https://raw.githubusercontent.com/G1oow/singbox-lite/main/singbox.sh'

step() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }
ok()   { printf '    \033[32mok\033[0m %s\n' "$*"; }
warn() { printf '    \033[33mwarn\033[0m %s\n' "$*" >&2; }
die()  { printf '\n\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }

# 命令执行器：DRY_RUN=1 时只回显
run() {
  if [[ $DRY_RUN == 1 ]]; then
    printf '    [dry] %s\n' "$*"
  else
    eval "$*"
  fi
}

need_root() {
  [[ ${VPS_INIT_ALLOW_NONROOT:-0} == 1 ]] && { warn "已跳过 root 检查"; return 0; }
  [[ ${EUID:-} == 0 ]] || die "请用 root 运行（或 sudo -i）"
}

backup_file() {
  local f=$1
  [[ -e $f ]] || return 0
  if [[ $DRY_RUN == 1 ]]; then
    printf '    [dry] backup %s\n' "$f"
    return 0
  fi
  mkdir -p "$BACKUP_ROOT"
  cp -a "$f" "$BACKUP_ROOT/$(basename "$f").$STAMP"
}

reload_sshd() {
  if systemctl is-active --quiet ssh.socket 2>/dev/null; then
    run "systemctl restart ssh.socket"
  elif systemctl list-unit-files ssh.service >/dev/null 2>&1; then
    run "systemctl reload ssh"
  else
    run "systemctl reload sshd"
  fi
}

collect_pubkey() {
  if [[ -n $SSH_PUBKEY ]]; then
    printf '%s\n' "$SSH_PUBKEY"
  elif [[ -n $SSH_PUBKEY_FILE ]]; then
    [[ -r $SSH_PUBKEY_FILE ]] || die "读不到 $SSH_PUBKEY_FILE"
    cat "$SSH_PUBKEY_FILE"
  else
    die "未提供公钥。用 SSH_PUBKEY='ssh-ed25519 ...' 或 SSH_PUBKEY_FILE=/path/to/key.pub"
  fi
}

# ---------------------------------------------------------------- 阶段 0
stage0() {
  need_root
  step "阶段 0 / SSH 加固：公钥 + 端口 $SSH_PORT"

  local key
  key="$(collect_pubkey)"
  [[ $key == ssh-* ]] || die "公钥格式不像 OpenSSH 公钥：${key:0:20}..."

  mkdir -p /root/.ssh
  chmod 700 /root/.ssh
  touch "$AUTH_KEYS"
  chmod 600 "$AUTH_KEYS"
  if grep -qF "$key" "$AUTH_KEYS"; then
    ok "公钥已存在，跳过"
  else
    backup_file "$AUTH_KEYS"
    printf '%s\n' "$key" >>"$AUTH_KEYS"
    ok "公钥已写入 $AUTH_KEYS"
  fi

  # 端口：优先处理 Ubuntu 的 socket activation，否则改 sshd_config
  if systemctl is-enabled --quiet ssh.socket 2>/dev/null; then
    warn "检测到 ssh.socket（socket 激活），端口必须写在 socket drop-in 里，改 sshd_config 无效"
    install -d -m 755 /etc/systemd/system/ssh.socket.d
    backup_file "$SOCKET_DROPIN"
    run "printf '[Socket]\nListenStream=\nListenStream=%s\n' '$SSH_PORT' > '$SOCKET_DROPIN'"
    run "systemctl daemon-reload"
  else
    backup_file "$SSHD_CONFIG"
    if ! grep -qE "^[[:space:]]*Port[[:space:]]+$SSH_PORT\b" "$SSHD_CONFIG"; then
      run "sed -i -E '/^[[:space:]]*#?[[:space:]]*Port[[:space:]]+[0-9]+/d' '$SSHD_CONFIG'"
      run "{ echo 'Port $SSH_PORT'; cat '$SSHD_CONFIG'; } > '$SSHD_CONFIG.new' && mv '$SSHD_CONFIG.new' '$SSHD_CONFIG'"
      ok "sshd_config 端口已设为 $SSH_PORT"
    else
      ok "sshd_config 已是 $SSH_PORT，跳过"
    fi
  fi

  # 防火墙：只动已启用的 ufw，避免凭空引入规则
  if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -qi '^Status: active'; then
    run "ufw allow $SSH_PORT/tcp"
    ok "ufw 已放行 $SSH_PORT/tcp"
  else
    warn "未检测到启用中的 ufw，iptables/nftables 规则需自行放行 $SSH_PORT/tcp"
  fi

  # 配置校验：失败就回滚，绝不带着坏配置重载
  if [[ $DRY_RUN != 1 ]]; then
    if systemctl is-enabled --quiet ssh.socket 2>/dev/null; then
      systemd-analyze verify "$SOCKET_DROPIN" >/dev/null 2>&1 || { warn "socket drop-in 校验失败，回滚"; rollback_socket; die "已回滚"; }
    else
      sshd -t || { warn "sshd -t 校验失败，回滚"; rollback_sshd; die "已回滚"; }
    fi
  fi

  reload_sshd
  [[ $DRY_RUN == 1 ]] || (ss -lntp 2>/dev/null | grep -q ":$SSH_PORT" && ok "端口 $SSH_PORT 已在监听") || warn "未看到 $SSH_PORT 处于监听，重载后稍等再查：ss -lntp | grep $SSH_PORT"

  cat <<EOF

    下一步（保持当前会话不要断开）：
      1. 云厂商安全组/防火墙放行 TCP $SSH_PORT
      2. 本地新开一个终端验证：ssh -p $SSH_PORT root@<VPS_IP>
      3. 登录成功后再执行：./vps-init.sh 0b

EOF
}

# ---------------------------------------------------------------- 阶段 0b
stage0b() {
  need_root
  step "阶段 0b / 关闭密码登录"

  [[ -s $AUTH_KEYS ]] || die "authorized_keys 为空，先跑阶段 0"
  [[ $DRY_RUN == 1 ]] || warn "当前未验证新端口可用，确认已能 ssh -p $SSH_PORT 登入后再继续"

  # 主配置 + 所有 drop-in（云镜像常把 PasswordAuthentication yes 写在 drop-in 里，
  # 而 sshd 采用「首次出现生效」，只改主配置会被 drop-in 覆盖）
  local f
  backup_file "$SSHD_CONFIG"
  run "sed -i -E 's/^([[:space:]]*#?[[:space:]]*PasswordAuthentication[[:space:]]+).*/\1no/' '$SSHD_CONFIG'"
  run "grep -qiE '^[[:space:]]*PasswordAuthentication[[:space:]]+no' '$SSHD_CONFIG' || echo 'PasswordAuthentication no' >> '$SSHD_CONFIG'"

  shopt -s nullglob
  for f in /etc/ssh/sshd_config.d/*.conf; do
    backup_file "$f"
    run "sed -i -E 's/^([[:space:]]*PasswordAuthentication[[:space:]]+).*/\1no/' '$f'"
    run "sed -i -E 's/^([[:space:]]*KbdInteractiveAuthentication[[:space:]]+).*/\1no/' '$f'"
    run "sed -i -E 's/^([[:space:]]*ChallengeResponseAuthentication[[:space:]]+).*/\1no/' '$f'"
    grep -qiE '^[[:space:]]*PasswordAuthentication[[:space:]]+no' "$f" \
      || run "echo 'PasswordAuthentication no' >> '$f'"
    ok "已加固 $f"
  done
  shopt -u nullglob

  # root 登录限制为仅密钥
  run "sed -i -E 's/^([[:space:]]*#?[[:space:]]*PermitRootLogin[[:space:]]+).*/\1prohibit-password/' '$SSHD_CONFIG'"
  run "grep -qiE '^[[:space:]]*PermitRootLogin' '$SSHD_CONFIG' || echo 'PermitRootLogin prohibit-password' >> '$SSHD_CONFIG'"

  if [[ $DRY_RUN != 1 ]]; then
    sshd -t || { warn "sshd -t 失败，回滚"; rollback_sshd; die "已回滚"; }
  fi
  reload_sshd
  if [[ $DRY_RUN != 1 ]]; then
    local eff
    eff="$(sshd -T 2>/dev/null | grep -iE '^(passwordauthentication|kbdinteractiveauthentication|permitrootlogin)' || true)"
    printf '%s\n' "$eff" | sed 's/^/    /'
    grep -qi '^passwordauthentication no' <<<"$eff" && ok "密码登录已关闭" || warn "生效值仍为 yes，检查 drop-in 顺序"
  fi
}

# ---------------------------------------------------------------- 阶段 1-5
stage1() {
  step "阶段 1 / TCP 质量检测（TcpQuality）"
  warn "会跑较长，建议在 tmux/screen 里执行，掉线不丢结果"
  run "bash <(curl -fsSL '$SRC_TCPQUALITY')"
}

stage2() {
  step "阶段 2 / 综合跑分（NodeQuality）"
  warn "在临时系统里跑，结束后清理，不影响本机环境"
  run "bash <(curl -sL '$SRC_NQ')"
}

stage3() {
  step "阶段 3 / TCP 调优（tcpfit）"
  warn "首次运行装到 /usr/local/bin/tcpfit 并弹菜单，选 1 为全自动；改动可用 tcpfit rollback 撤销"
  if command -v tcpfit >/dev/null 2>&1; then
    ok "tcpfit 已安装，直接进入菜单"
    run "tcpfit"
  else
    run "bash <(curl -fsSL '$SRC_TCPFIT')"
  fi
}

stage4() {
  step "阶段 4 / Reality 伪装目标筛选（reality-checker）"
  local ip
  ip="$(curl -fsS --max-time 8 https://icanhazip.com 2>/dev/null || true)"
  [[ -n $ip ]] || die "取不到公网 IP，先手动跑：cd /root/rc-new && ./reality-checker-main auto <你的VPS_IP> --limit 5"
  if [[ ! -x /root/rc-new/reality-checker-main ]]; then
    run "curl -fsSL '$SRC_RC_INSTALL' | bash"
  fi
  ok "本机公网 IP：$ip"
  run "cd /root/rc-new && ./reality-checker-main auto '$ip' --limit 5"
  warn "从中挑 5 星里握手最低的 2~3 个，配节点前先各自试连"
}

stage5() {
  step "阶段 5 / sing-box + Xray 面板（singbox-lite）"
  if [[ -x /usr/local/bin/sb ]]; then
    ok "已安装，直接启动"
  else
    run "(curl -LfsS '$SRC_SB' -o /usr/local/bin/sb || wget -q '$SRC_SB' -O /usr/local/bin/sb) && chmod +x /usr/local/bin/sb"
  fi
  if [[ $DRY_RUN == 1 ]]; then run "sb"; else exec /usr/local/bin/sb; fi
}

# ---------------------------------------------------------------- 回滚
rollback_sshd() {
  local latest
  latest="$(ls -1t "$BACKUP_ROOT"/sshd_config.* 2>/dev/null | head -n1 || true)"
  [[ -n $latest ]] || die "没有 sshd_config 备份"
  cp -a "$latest" "$SSHD_CONFIG"
  ok "已恢复 $latest"
  sshd -t && { reload_sshd; ok "已重载"; }
}

rollback_socket() {
  local latest
  latest="$(ls -1t "$BACKUP_ROOT"/10-vps-init-port.conf.* 2>/dev/null | head -n1 || true)"
  if [[ -n $latest ]]; then cp -a "$latest" "$SOCKET_DROPIN"; else rm -f "$SOCKET_DROPIN"; fi
  systemctl daemon-reload
  systemctl restart ssh.socket
  ok "socket drop-in 已回滚"
}

rollback() {
  need_root
  step "回滚 SSH 配置"
  if systemctl is-enabled --quiet ssh.socket 2>/dev/null; then rollback_socket; else rollback_sshd; fi
  warn "备份目录保留在 $BACKUP_ROOT，确认无误后可删"
}

usage() {
  sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
}

main() {
  local stage="${1:-}"
  [[ -n $stage ]] || { usage; exit 1; }
  case $stage in
    0) stage0 ;;
    0b) stage0b ;;
    1) stage1 ;;
    2) stage2 ;;
    3) stage3 ;;
    4) stage4 ;;
    5) stage5 ;;
    rollback) rollback ;;
    -h|--help|help) usage ;;
    *) usage; die "未知阶段：$stage" ;;
  esac
}

main "$@"
