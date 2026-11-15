# 🛡️ Reality 目标检测（reality-checker）

[![Build and Release](https://github.com/G1oow/reality-checker-main/actions/workflows/build.yml/badge.svg)](https://github.com/G1oow/reality-checker-main/actions/workflows/build.yml)
![Platform](https://img.shields.io/badge/platform-linux%20amd64%20%7C%20arm64-2ea44f)
![Go](https://img.shields.io/badge/go-1.26-00ADD8)

扫描并筛选适合作为 **Xray / sing-box REALITY 伪装目标（fallback SNI）** 的真实站点域名。

程序会从一个 ASN 或 CIDR 网段出发，逐 IP 发起真实 TLS 握手，从站点证书里挖出候选域名，再按硬性技术底线逐个校验，最后按是否 CDN、是否热门大站、握手延迟、证书剩余天数、页面状态综合打星，只输出真正可用的目标。

安装后一条 `rcm` 即可进入 **VPS 终端面板**：按国家/地区 ASN 或按本机入口 ASN 扫描，实时看进度、随手停。

> [!IMPORTANT]
> 请只使用本仓库 Release 的产物。上游流传的 `v2.2.3`（`csv` 命令只取第 3 列）与 `v1.1.2`（证书校验 bug，结果恒为 0）均不可用；相关 bug 已在 `src/` 对应分支修复。

> [!NOTE]
> 本项目只支持 **Linux（amd64 / arm64）**。Windows 端的安装脚本、构建脚本与可执行产物已从仓库移除。

## 检测标准

只有通过全部硬性条件的域名才会被纳入结果：

- **协议与密码学**：TLS 1.3、X25519（含 `X25519MLKEM768` 混合曲线）、HTTP/2 协商成功；
- **证书**：链路校验通过、SNI 与证书主机名严格匹配、剩余有效期高于阈值；
- **网络**：真实握手延迟低于阈值，页面状态码不在排除列表；
- **选型偏好**：默认剔除公共 CDN、热门大站、国内站点、Nginx / OpenResty 等默认欢迎页，以及 `default backend`、`tuwunel` 之类极短占位回显；
- **归属地**：基于 GeoIP 判定目标国家，避免跨区流量特征异常。

## 一键安装

```bash
curl -fsSL https://raw.githubusercontent.com/G1oow/reality-checker-main/main/install.sh | bash
```

默认安装到 `/root/rc-new`，并把快捷指令 `rcm` 装到 `/usr/local/bin`。安装脚本会：

1. 从 Release 下载主程序 `reality-checker-main` 并 `chmod +x`；
2. 拉取 `config.yaml` 与 `data/` 下的离线规则库（GeoIP 国家库、GFW 列表、CDN 特征、热门站点特征、排除规则）；
3. 安装 `rcm` 快捷指令（下载失败时使用脚本内置兜底版本）；
4. 在交互终端里**直接进入面板**；非交互环境（`curl | bash`、CI）只打印后续用法并正常退出，不会阻塞。

自定义安装目录：

```bash
curl -fsSL https://raw.githubusercontent.com/G1oow/reality-checker-main/main/install.sh | bash -s -- /opt/rc
```

可用环境变量：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `REF` | `main` | 下载 `config.yaml` 与规则库的分支 |
| `REF_TAG` | `latest` | 主程序的 Release 版本 tag |
| `RC_BASE` | GitHub raw | 规则库与配置下载基址（便于走镜像） |
| `RC_REL_BASE` | GitHub Releases | 主程序下载基址 |
| `RC_BIN_DIR` | `/usr/local/bin` | `rcm` 安装目录 |
| `RC_NO_PANEL` | 空 | 置 `1` 时安装完成后不自动进入面板 |

不愿意 `curl | bash` 的可以手动部署：把 Release 里的 `reality-checker-main` 与仓库的 `config.yaml`、`data/` 放进同一目录，`chmod +x` 后执行 `./reality-checker-main ui` 即可。

## 终端面板 `rcm`

```bash
rcm            # 进入面板，沿用上次保存的扫描参数
rcm --help     # 查看面板全部选项与菜单操作
```

面板是**交互式数字菜单**（singbox-lite 那类风格）：每轮清屏重绘「状态 + 分组编号菜单」，直接输入编号进入对应设置或执行动作，既不需要方向键，也不占用终端的备用屏，SSH、手机终端、tmux 下表现一致。

```text
  ╔══════════════════════════════════════════════════════════════════╗
  ║  R-C-M · Reality Checker v1.8.0                                  ║
  ║  伪装目标扫描面板 · 输入编号操作                                 ║
  ╚══════════════════════════════════════════════════════════════════╝

  本机出口   ✔ IPv4 203.0.113.7 / AS64500 / DE / 机房  |  ! IPv6 不可用（无 IPv6 出口）
  发现来源   按 VPS 入口 IP ASN（发现用 IPv4 网段，域名按 IPv4 + IPv6 校验）
  运行状态   空闲：先用 1~13 调整参数，再按 s 开始扫描

  【扫描参数】
    [1]    扫描模式       按 VPS 入口 IP ASN
    [2]    校验协议族     IPv4 + IPv6
    [3]    国家/地区代码  —（仅国家模式 / 仅 IPv6 校验使用）
    [4]    目标数量       5
    ...

  【执行】
    [s]    开始扫描       [d]    查看上次结果   [r]    重新探测出口
  ──────────────────────────────────────────────────────────────────
    [0]    退出面板（扫描参数已自动保存）

  请输入选项 [1-13 / s / d / r / 0]:
```

扫描分为两步：**发现**（扫 IPv4 网段，从站点证书里挖候选域名）与**校验**（按所选协议族验证候选域名）。

| 发现来源 | 触发条件 | 扫描对象 |
|---|---|---|
| 按 VPS 入口 IP ASN | 默认 | 自动探测本机出口 IPv4 / IPv6，取所选入口的 ASN，扫描该 ASN 公告的 IPv4 网段 |
| 按国家/地区 ASN | 「扫描模式」选到国家模式 | 在线拉取该国 ASN 清单（RIPE Stat `country-resource-list`），逐个 ASN 扫描其 IPv4 网段 |
| 按 VPS 所在国家 ASN | 「校验协议族」选「仅 IPv6」时固定使用 | 先由出口探测判定 VPS 所在国家（优先 IPv6 出口，回退 IPv4），再扫该国 ASN 清单的 IPv4 网段；填国家代码可覆盖自动判定 |

> [!NOTE]
> IPv6 不做地址空间扫描：运营商常见的 IPv6 聚合段（`/32`、`/41`）无法逐 IP 枚举，所以 IPv6 只参与**域名校验**。选「仅 IPv6」时域名必须有 `AAAA` 记录，并用 `tcp6` 完成 TLS 1.3 / HTTP/2 / 证书校验，没有 AAAA 的域名会被剔除并显示原因；本机没有 IPv6 出口时会直接阻止启动，避免空跑。发现阶段只扫 `IPv4 ≥ /16` 的网段，更粗的聚合段会跳过并说明原因。

面板内可以：

- **改参数**：输入 `1`~`13` 进入对应项的取值菜单（是/否 类直接选编号，数值类直接输入，回车保留当前值、`0` 或 `b` 返回）；
- **一键扫描**：`s` 开始，扫描中按 `Ctrl+C` 只停止本次扫描并回到菜单，命中数达到 limit 立即停止；
- **看进度**：已扫描 IP 数、命中数、候选域名数、速率、当前网段、当前 IP、批次进度、已用时长实时打印；
- **看结果**：`d` 查看上次扫描结果表（域名 / IP / ASN / 国家 / 星级 / 握手延迟 / 证书剩余天数 / 是否 CDN / 状态码，按星级与延迟排序）；
- **重探出口**：`r` 重新探测本机出口 IPv4 / IPv6 与 ASN；`0` 退出面板。

**参数持久化**：改动后立即保存到 `~/.config/reality-checker/panel.json`（可用 `RCM_SETTINGS` 或 `--settings` 指定其他路径），下次进入自动沿用；删除该文件即恢复默认。

**结果只展示、不落库**：面板扫描不读写资产数据库（`data/reality_targets.db`），需要入库时用 `auto` / `get`。

面板也支持纯文本模式，便于脚本或无终端环境：

```bash
./reality-checker-main ui --no-tui --mode country --country DE --limit 3
./reality-checker-main ui --family v6 --limit 1     # 仅 IPv6 校验：按 VPS 所在国家 ASN 的 IPv4 网段发现
```

面板的完整字段说明、纯文本输出样例见 [`src/doc/deploy/tui-panel.md`](./src/doc/deploy/tui-panel.md)。

## 命令行

| 命令 | 作用 |
|---|---|
| `ui` | 进入 VPS 终端面板（推荐，`rcm` 即等价入口） |
| `get <ip>` | 以指定 IP 极速获取优质伪装目标（缓存优先 + 流式现扫） |
| `auto <ip/cidr>` | 按入口 IP 的 ASN 自动拉取网段并并发扫描（支持 `--in` 读文件） |
| `asn <ASN> <国家>` | 查询 ASN 网段并按国家过滤，或管理 ASN 注册表 |
| `serve` | 启动 Headless REST / SSE 服务（含 Web 控制台） |
| `daemon` | 启动全网 ASN 自动化扫描守护进程 |
| `patrol` | 对资产库历史目标执行健康复核与生命周期维护 |
| `pipe` | 从 stdin 流式读取 RealiTLScanner 结果或域名列表 |
| `check <domain>` | 检测单个域名 |
| `batch <d1> <d2> ...` | 并发检测多个域名 |
| `migrate` | 在 SQLite 与 PostgreSQL 之间迁移资产数据 |
| `version` | 显示版本、提交与构建信息 |

全局选项：`-c/--config FILE` 指定配置文件，`-f/--force` 跳过启动时的出口 IP 探测。

### 常用示例

```bash
# 全自动：查 ASN -> 拉同国家网段 -> 并发扫描 -> 按星级输出
./reality-checker-main auto 198.51.100.10 --limit 5

# 全量摸底并导出 JSON（不提前刹车，结果入库）
./reality-checker-main auto 198.51.100.10 --check-all --export targets.json

# 查某 ASN 在某国的网段，再按文件扫描
./reality-checker-main asn AS15169 US > asn.txt
./reality-checker-main auto --in asn.txt --limit 10

# 流式检测 RealiTLScanner 的 CSV 结果
cat file.csv | ./reality-checker-main pipe

# 检测单个域名 / 批量域名
./reality-checker-main check example.com
./reality-checker-main batch apple.com tesla.com microsoft.com

# 只要 IPv6 目标
./reality-checker-main auto 2001:db8::/64 --limit 5 -6
```

输入单个 IP 时，IPv4 会扩展为所在 `/24`，IPv6 会扩展为所在 `/64`；输入 CIDR 则直接扫描该网段。

### `auto` 常用参数

| 参数 | 说明 |
|---|---|
| `-m, --limit N` | 保留的合格目标数量上限（默认 5） |
| `-a, --check-all` | 全量摸底，不提前终止，结果入库 |
| `--reset-scan` | 重置断点记录，从第 1 个网段重新开始 |
| `-t, --threads N` | 扫描并发协程数（默认 200，高性能 VPS 可设 500~1000） |
| `--timeout SEC` | 单 IP 握手超时秒数（默认 3，死 IP 多时用 1~2） |
| `--no-cache` / `--recheck` | 跳过本地缓存 / 对缓存命中项发起在线复核 |
| `--export FILE` | 把本次发现的合格资产导出为 JSON |
| `--min-stars` `--max-handshake` `--min-cert-days` | 星级、握手延迟、证书天数阈值 |
| `--allow-cdn` `--allow-hot` | 放行 CDN 节点 / 热门大站 |
| `--country CODE` | 指定国家 ISO 代码（如 `US`、`DE`） |
| `-4` / `-6` | 仅 IPv4 / 仅 IPv6 |
| `--debug` | 输出逐 IP 调试日志 |

完整参数以 `reality-checker-main --help` 与各子命令 `--help` 为准。

### 输出约定

人类可读信息（进度、状态、警告、错误）走 **stderr**，机器可读数据（`pipe` 数据流、`--json`、`asn` 的 CIDR 列表）走 **stdout**，因此 `asn AS15169 US > asn.txt` 拿到的永远是纯网段列表。输出格式规范见 [`src/doc/dev/cli-output-style.md`](./src/doc/dev/cli-output-style.md)。

典型结果表：

```text
适合的域名:
+------------------------------------+----------+----------+----------+-----+------+------+----------+
| 最终域名                           | 基础条件 | 握手时间 | 证书时间 | CDN | 热门 | 推荐 | 页面状态 |
+------------------------------------+----------+----------+----------+-----+------+------+----------+
| https://mirror-csail.debian.org    |     ✓    |   282ms  |   87天   |  无 |   -  | **** |    200   |
| https://specimens.avrenela.com     |     ✓    |   285ms  |   79天   |  无 |   -  | **** |    200   |
+------------------------------------+----------+----------+----------+-----+------+------+----------+
```

挑选时只看 **推荐星级**：能拿到 5 星就用 5 星，同星级选手握时间最低的；正式配置前把前 2~3 个都试连一次。

## 配置文件

主程序启动时读取同目录下的 `config.yaml`（也可用 `-c/--config` 指定），改完立即生效，无需重新编译。

| 配置段 | 关键项 |
|---|---|
| `log` | `level`（debug/info/warn/error）、`file` |
| `database` | `driver`（`sqlite` / `postgres`）、`sqlite.path`、`postgres.*`（含 `dsn`、连接池） |
| `reality_filter` | `require_no_cn`、`check_gfw`、`require_no_cdn`、`require_no_hot`、`require_no_default_page`、`max_handshake_ms`、`min_cert_days`、`min_stars`、`use_cache`、`cache_max_days`、`scan_threads`、`scan_timeout`、`exclude_*` / `include_suffixes` |
| `proxy` | `url`（`http://` / `https://` / `socks5://`）、`download`（下载是否走代理）、`scanner`（扫描是否走代理） |
| `network` | `timeout`、`retries`、`dns_servers`、`family`（`""` / `ipv4` / `ipv6`） |
| `concurrency` | `max_concurrent`、`check_timeout` |

> [!WARNING]
> 仓库自带的 `config.yaml` 把 `database.driver` 设成了 `postgres`，而存储层不会自动回退。单机部署若没有 PostgreSQL，请改成 `sqlite`，否则打开资产库会失败（部分命令会退化为纯扫描模式）。

> [!TIP]
> 从国内网络访问 GitHub / GeoIP 源受限时，先在 `config.yaml` 的 `proxy.url` 填直连出口对应的入站，再把 `proxy.download` 置 `true`；`proxy.scanner` 建议保持 `false`，否则代理不通会直接影响 ASN 与归属地查询，握手延迟也不再代表真实体验。

排除规则可从外部文件注入（默认 `data/exclude_rules.txt`），与配置内的 `exclude_domains`、`exclude_suffixes`、`exclude_patterns`、`exclude_status` 叠加生效。

## Web 控制台与 REST API

`serve` 或 `daemon` 会启动 HTTP 服务（默认 `0.0.0.0:8881`），同时提供内嵌的 React 控制台（`go:embed`，单二进制独立分发）与 REST / SSE 接口：

```bash
./reality-checker-main serve --port 8881 --token <YOUR_API_TOKEN>
```

| 接口 | 说明 |
|---|---|
| `GET /api/health` | 健康检查 |
| `GET /api/stats` | 资产库统计 |
| `GET /api/filter/default` | 当前默认过滤策略 |
| `GET /api/targets/smart` | 按目标 IP 动态加权打分推荐（同 ASN 置顶） |
| `GET /api/targets/stream` | SSE 流式推送扫描结果 |
| `POST /api/targets/check` | 即时检测指定域名 |
| `GET/POST /api/targets` | 资产列表与写入 |
| `/api/targets/<id>` | 单个资产操作 |
| `GET/POST /api/asns` | ASN 扫描队列 |

鉴权 Token 取值顺序：`--token` → 环境变量 `RC_API_TOKEN` → `config.yaml` 的 `server.api_token`；留空则不开启鉴权。

## 守护进程（systemd）

仓库自带 systemd 部署脚本（[`src/scripts/install.sh`](./src/scripts/install.sh)），用于把常驻守护进程装成服务：

```bash
sudo bash src/scripts/install.sh --repo G1oow/reality-checker-main --token <YOUR_API_TOKEN> -t 150
```

`--repo` 请显式指向本仓库：脚本默认值仍是上游 `qualvey/one-reality`，用默认值会装到上游产物。

脚本会安装二进制与别名 `reasni`、创建 `/etc/reasni`（配置）与 `/var/lib/reasni`（数据）、注册 `reasni-daemon.service`，并生成运维快捷命令：

```bash
reasni-service status     # 查看运行状态
reasni-service log        # 跟踪扫描日志
reasni-service restart    # 重启
reasni-service config     # 编辑启动参数与 Token（/etc/default/reasni）
```

启动参数、单元文件模板见 [`src/deploy/systemd/README.md`](./src/deploy/systemd/README.md) 与 [`src/scripts/install.sh`](./src/scripts/install.sh)。单板设备（NanoPi / 树莓派 / Armbian / 内存 < 2GB）建议 `-t 100~150`，避免占满家用路由的 NAT 连接表。

## 从源码构建

```bash
cd src
./build.sh          # 产物输出到 src/dist/（linux/amd64、linux/arm64）
```

环境要求：**Go 1.26+**、**pnpm**（或 npm）、**Node 22+**。构建顺序不能颠倒 —— `web/dist` 是 `go:embed` 的编译依赖，`build.sh` 会先构建前端再编译 Go，否则报 `pattern dist/*: no matching files found`。脚本还会校验（缺失则下载）`internal/data/assets/Country.mmdb` 用于内置 GeoIP；国内网络请先在配置里设好代理。

产物默认名为 `reality-checker`，需要与安装脚本对齐时用 `APP_NAME=reality-checker-main` 覆盖。构建脚本同时把 `install.sh`、`reasni-daemon.service`、`rcm` 复制进 `dist/`。

**发布流程**：产物不提交进仓库。推 tag（`git tag v1.0.0 && git push origin v1.0.0`）触发 [`.github/workflows/build.yml`](./.github/workflows/build.yml) 构建 Linux 产物并发布 Release，`install.sh` 从 Release 下载。

**架构文档**：源码目录带一套 VitePress 文档站：

```bash
cd src
pnpm install        # 首次先装依赖
pnpm docs:dev       # 本地预览（doc/）
pnpm web:dev        # Web 控制台开发服务器
```

`src/doc/` 内含 [TUI 面板](./src/doc/deploy/tui-panel.md)、[守护进程部署](./src/doc/deploy/systemd-daemon.md)、[24 小时 ASN 扫描](./src/doc/dev/asn-24h-scanner.md)、[数据库与生命周期](./src/doc/dev/database-and-lifecycle.md)、[CLI 输出风格](./src/doc/dev/cli-output-style.md) 等专题。

## 常见问题

| 现象 | 处理 |
|---|---|
| `Permission denied` | `chmod +x reality-checker-main`（每次 `scp` 后都要重做） |
| `rcm: command not found` | 用 `RC_HOME=/root/rc-new rcm`，或重跑 `install.sh` 安装快捷指令 |
| 启动即报出口探测失败 | 确认网络后重试，或追加 `--force` / `-f` 跳过探测 |
| 结果恒为 0 | 先 `check <已知可用的域名>` 自检；目标机房全是 CDN 时加 `--allow-cdn` |
| 扫描很慢 | 降低 `--timeout`；1 核机器并发别超过 200 |
| 程序卡死 | 另开终端 `kill -QUIT $(pidof reality-checker-main)` 抓协程堆栈，再调低 `--threads` |
| 规则库下载失败 | 确认 `data/` 五个文件都在，或让 `proxy.download` 走代理 |
| 资产库打开失败 | 检查 `database.driver` 与实际数据库是否匹配（见上方警告） |
| 面板参数想恢复默认 | 删除 `~/.config/reality-checker/panel.json` |

> [!NOTE]
> `data/` 目录必须与主程序同级。缺失时程序会尝试联网拉取，网络不好就会失败，这也是手动部署最容易踩的坑。

## 仓库结构

| 路径 | 说明 |
|---|---|
| `install.sh` | 一键部署脚本（装完直接进面板） |
| `rcm` | 面板快捷启动器；按 `RC_HOME` → 脚本目录 → `/root/rc-new` → `~/rc-new` → `/opt/rc` → `/usr/local/rc` 顺序定位主程序 |
| `config.yaml` | 过滤策略与运行参数 |
| `data/` | 离线规则库：`Country.mmdb`、`gfwlist.conf`、`cdn_keywords.txt`、`hot_websites.txt`、`exclude_rules.txt` |
| `file.csv` | 历史扫描结果样例（已脱敏），可直接 `pipe` 喂入 |
| `vps-init.sh` | 新 VPS 开荒脚本（公钥/改端口/测速/调优/选目标/建节点，分段幂等） |
| `src/` | 完整源码：Go 后端 + React 控制台 + VitePress 文档 + `build.sh` |
| `.github/workflows/build.yml` | 推 tag 自动构建 Linux 产物并发布 Release |

---

**免责声明**：本工具仅用于网络技术研究与学习，请遵守当地法律法规，合理使用网络资源。
