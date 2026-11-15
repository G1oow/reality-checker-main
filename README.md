# Reality 目标检测（reality-checker）

自动扫描并筛选适合做 **Xray / sing-box REALITY 伪装目标（fallback SNI）** 的真实网站域名。

程序会硬性校验 TLS 1.3、X25519、HTTP/2、证书有效性、SNI 匹配、非国内站点，并按是否 CDN、是否热门大站、握手延迟、证书剩余天数、页面状态综合打星，只输出真正可用的目标。

**面向 VPS 的终端面板（TUI）**：装完一条 `rcm` 就能进面板，按国家/地区 ASN 或按本机入口 IP 的 ASN 扫描，实时看进度、随手停。

> 配套程序由 **GitHub Actions 从 `main` 构建并发布到 Releases**。上游流传的 `v2.2.3`（csv 命令只取第 3 列）与 `v1.1.2` release（证书校验 bug，结果恒为 0）**均不可用**，请只使用本仓库的 release。
>
> 本仓库**只支持 Linux**（amd64 / arm64）。Windows 端支持已移除：相关安装脚本、构建脚本与可执行产物均已从仓库删除。

---

## 一键指令

```bash
curl -fsSL https://raw.githubusercontent.com/G1oow/reality-checker-main/main/install.sh | bash
```

默认安装到 `/root/rc-new`，并安装快捷指令 `rcm` 到 `/usr/local/bin/rcm`。安装脚本会：

1. 下载主程序（`reality-checker-main`）并 `chmod +x`；
2. 拉取 `config.yaml` 与 `data/` 下的离线规则库（国家 IP 库、GFW 列表、CDN 特征、热门站点特征、排除规则）；
3. 安装 `rcm` 快捷指令（下载失败时使用脚本内置兜底版本）；
4. **在交互终端里直接进入面板**；非交互环境（`curl | bash`、CI）只打印后续用法并正常退出，绝不阻塞。

自定义目录：

```bash
curl -fsSL https://raw.githubusercontent.com/G1oow/reality-checker-main/main/install.sh | bash -s -- /opt/rc
```

安装脚本支持的环境变量：`REF` / `REF_TAG`（规则库分支与主程序版本）、`RC_BASE` / `RC_REL_BASE`（下载基址覆盖，便于走镜像）、`RC_BIN_DIR`（快捷指令安装目录，默认 `/usr/local/bin`）、`RC_NO_PANEL=1`（装完不自动进面板）。

---

## 面板：`rcm`

```bash
rcm            # 进入终端面板，沿用上次保存的扫描参数
rcm --help     # 查看面板全部选项与快捷键
```

面板细节（快捷键、过滤项含义、参数持久化、纯文本模式）见 [`src/doc/deploy/tui-panel.md`](./src/doc/deploy/tui-panel.md)。

面板有两个扫描模式：

| 模式 | 入口 IP 的来源 | 扫描对象 |
|---|---|---|
| **按 VPS 入口 IP ASN** | 自动探测本机出口 **IPv4 与 IPv6**，分别解析各自 ASN 与国家，由你选择用哪个（或两者） | 该入口所属 ASN 的已公告网段 |
| **按国家/地区 ASN** | 不需要入口 IP | 在线拉取该国 ASN 清单（RIPE Stat `country-resource-list`），逐个 ASN 拉取网段后扫描 |

面板内可以做的事：

- **一键扫描**：按 `s` 开始，按 `x` / `esc` 随时停止，命中数量达到 limit 立即停止；
- **手动选择 limit**：`1 2 3 5 8 10 20 30 50 100`，上限 100；
- **扫描参数**：并发（10~2000）、单 IP 超时（1~15 秒）；
- **进阶过滤**：最低星级、握手延迟上限、证书剩余天数下限、是否允许 CDN、是否允许热门大站、是否排除国内站点、是否排除默认页；
- **实时进度**：已扫描 IP 数、命中数、候选域名数、速率、当前网段、当前 IP、批次进度、已用时长；
- **结果展示**：域名 / IP / ASN / 国家 / 星级 / 握手延迟 / 证书剩余天数 / 是否 CDN / 状态码，按星级与延迟排序，可滚动。

**快捷键**：`↑↓` 选择参数 · `←→` 改值 · `空格` 切换开关 · `回车` 输入国家代码 · `tab` 切换焦点（参数区 ↔ 结果区）· `s` 开始扫描 · `x`/`esc` 停止 · `r` 重新探测入口 · `q` 退出。

**参数持久化**：面板参数自动保存到 `~/.config/reality-checker/panel.json`（可用 `RCM_SETTINGS` 或 `--settings` 指定其他路径），下次进入面板时沿用。

**结果只展示、不落库**：面板扫描不会读写资产数据库（`data/reality_targets.db`），需要入库时使用 `auto` / `get` 等命令。

面板也支持纯文本模式，便于脚本或无终端环境：

```bash
./reality-checker-main ui --no-tui --mode country --country DE --limit 3
./reality-checker-main ui --family v6 --limit 1     # 仅用 IPv6 入口，命中 1 个即停
```

---

## 手动部署

```bash
mkdir -p /root/rc-new && cd /root/rc-new
curl -fL -o reality-checker-main \
  https://github.com/G1oow/reality-checker-main/releases/latest/download/reality-checker-main
chmod +x reality-checker-main
rcm                                     # 若未安装 rcm，可直接 ./reality-checker-main ui
```

指定版本时把 `latest` 换成对应 tag：`.../releases/download/<tag>/reality-checker-main`。

**注意：`data/` 目录必须与主程序同级**，否则启动时会尝试从 GitHub 拉规则库，网络不好就会失败。

---

## 源码与自编译

完整源码在 [`src/`](./src)，取自上游 [qualvey/one-reality](https://github.com/qualvey/one-reality) 的 main 分支 —— 即修复了 `v2.2.3` csv 解析 bug 与 `v1.1.2` 证书校验 bug 的那条分支。Go + React 双端。

```bash
cd src
./build.sh          # 产物输出到 src/dist/（Linux amd64 / arm64）
```

构建脚本会自动校验（缺失则下载）`internal/data/assets/Country.mmdb` 用于内置 GeoIP，国内网络先在 `src/config.yaml` 配好 `proxy` 再构建。产物默认名为 `reality-checker`，需要与其他脚本对齐时用 `APP_NAME=reality-checker-main` 覆盖。构建不依赖源码目录下的 `src/data/`；运行时若需外部规则库，直接读主程序同级的 `data/`。

**产物不再提交进仓库**：推 tag（`git tag v1.0.0 && git push origin v1.0.0`）会触发 `.github/workflows/build.yml` 自动构建 Linux 产物并发布 Release，`install.sh` 从 Release 下载。

**顺序要求**：`web/dist` 是 `go:embed` 的编译依赖，必须先构建前端再编译 Go，否则报 `pattern dist/*: no matching files found`。环境：Go 1.26+、pnpm。

---

## 常用命令

```bash
# 面板（推荐）
rcm

# 全自动：查 ASN -> 拉同国家网段 -> 并发扫描 -> 按星级输出
./reality-checker-main auto <IP/CIDR> --limit 5

# 流式检测 RealiTLScanner 的 CSV 结果
cat file.csv | ./reality-checker-main pipe

# 检测单个域名
./reality-checker-main check <已知可用的域名>

# 批量检测
./reality-checker-main batch apple.com tesla.com microsoft.com

# 查询某 ASN 的网段
./reality-checker-main asn AS15169 US > asn.txt
./reality-checker-main auto --in asn.txt --limit 10

# 只要 IPv6 目标
./reality-checker-main auto 2001:db8::/64 --limit 5 -6
```

`auto` 常用参数：`--limit/-m`、`--check-all/-a`、`--threads/-t`、`--timeout`、`--ipv4-only/-4`、`--ipv6-only/-6`、`--allow-cdn`、`--allow-hot`、`--min-stars`、`--max-handshake`、`--country`、`--export`、`-v`。

完整参数说明见 `reality-checker-main --help`，结果列含义见「挑选建议」。

**输出约定**：人类可读信息（进度、状态、警告、错误）走 stderr，机器可读数据（`pipe` 流、`--json`、`asn` 的 CIDR 列表）走 stdout，因此 `asn AS15169 US > asn.txt` 拿到的永远是纯网段列表。输出格式规范见 [`src/doc/dev/cli-output-style.md`](./src/doc/dev/cli-output-style.md)。

---

## 挑选建议

只看 **推荐星级** 列：能拿到 5 星就用 5 星；同星级选手握时间最低的；实际配置前把前 2~3 个都试连一次。

星级门槛、延迟上限、证书最低天数、CDN/热门过滤、代理等全部在 `config.yaml` 里，改完立即生效，无需重编译。

---

## 常见问题

| 现象 | 处理 |
|---|---|
| `Permission denied` | `chmod +x reality-checker-main`（每次 `scp` 后都要重做） |
| `rcm: command not found` | 用 `RC_HOME=/root/rc-new rcm` 或重新运行 `install.sh` 安装快捷指令 |
| 结果恒为 0 | 先 `./reality-checker-main check <已知可用的域名>` 自检；目标机房全是 CDN 时加 `--allow-cdn` |
| 扫描很慢 | 降低 `--timeout`，1 核机器并发别超过 200 |
| 程序卡死 | 另开终端 `kill -QUIT $(pidof reality-checker-main)` 抓协程堆栈，然后调低 `--threads` |
| 规则库下载失败 | 确认 `data/` 五个文件都在，或在 `config.yaml` 的 `proxy.download` 走代理 |
| 面板参数想恢复默认 | 删除 `~/.config/reality-checker/panel.json` |

---

## 文件说明

| 文件 | 说明 |
|---|---|
| `reality-checker-main` | Linux 主程序（从 Releases 下载，不再入库） |
| `rcm` | 面板快捷指令（`install.sh` 会安装到 `/usr/local/bin/rcm`） |
| `config.yaml` | 过滤策略与运行参数 |
| `data/` | 离线规则库（必须与主程序同级） |
| `file.csv` | 历史扫描结果样例（已脱敏，IP/域名均为示例值），可直接 `pipe` 喂入 |
| `install.sh` | 一键部署脚本（装完直接进面板） |
| `vps-init.sh` | 新 VPS 开荒脚本（公钥/改端口/测速/调优/选目标/建节点，分段幂等） |
| `src/` | 完整源码（Go + React，含 build.sh） |

> Windows 端支持已移除，相关脚本与产物不再随仓库分发；git 历史中仍留有旧提交对象，如需彻底清理需重建仓库。

---

**免责声明**：本工具仅用于网络技术研究与学习，请遵守当地法律法规，合理使用网络资源。
