# Reality 目标检测（reality-checker）

自动扫描并筛选适合做 **Xray / sing-box REALITY 伪装目标（fallback SNI）** 的真实网站域名。

程序会硬性校验 TLS 1.3、X25519、HTTP/2、证书有效性、SNI 匹配、非国内站点，并按是否 CDN、是否热门大站、握手延迟、证书剩余天数、页面状态综合打星，只输出真正可用的目标。

> 配套程序为 **main 分支自编译版**。GitHub 上流传的 `v2.2.3`（csv 命令只取第 3 列）与 `v1.1.2` release（证书校验 bug，结果恒为 0）**均不可用**，请勿下载 release 版本。

---

## 一键指令

装完会自动探测本机出口 IP：检测到双栈时会让你选 IPv4 还是 IPv6，选完回车即可开始扫描。

### Linux（VPS / Debian / Ubuntu）

```bash
curl -fsSL https://raw.githubusercontent.com/G1oow/reality-checker-main/main/install.sh | bash
```

默认安装到 `/root/rc-new`，完成后直接跑：

```bash
cd /root/rc-new && ./reality-checker-main auto <你的VPS_IP> --limit 5
```

自定义目录：

```bash
curl -fsSL https://raw.githubusercontent.com/G1oow/reality-checker-main/main/install.sh | bash -s -- /opt/rc
```

### Windows（PowerShell）

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/G1oow/reality-checker-main/main/install.ps1 | iex"
```

默认安装到 `%USERPROFILE%\reality-checker`，完成后直接跑：

```powershell
cd "$env:USERPROFILE\reality-checker"; .\reality-checker-main.exe auto <你的VPS_IP> --limit 5
```

自定义目录：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "& { $env:REF='main'; irm https://raw.githubusercontent.com/G1oow/reality-checker-main/main/install.ps1 -OutFile $env:TEMP\rc-install.ps1; & $env:TEMP\rc-install.ps1 'D:\rc' }"
```

### 一键指令做了什么

1. 下载对应平台的主程序（`reality-checker-main` / `reality-checker-main.exe`）；
2. 解除执行限制（Linux `chmod +x`，Windows `Unblock-File`）；
3. 拉取 `config.yaml` 与 `data/` 下的离线规则库（国家 IP 库、GFW 列表、CDN 特征、热门站点特征、排除规则）；
4. 自动探测本机出口 IP：只有 IPv4 或只有 IPv6 就直接采用；**双栈时让你选 1) IPv4 / 2) IPv6**（选 IPv6 会附加 `-6`）；都探测不到则提示手动输入；
5. 打印完整运行命令，问一句「现在开始扫描？」，回车即开始（默认不跑）。

全程只往指定目录写文件，不改动系统配置。全自动场景（无终端、`curl | bash` 到 CI）会自动降级为默认值，不会卡住。

> 探测走 `api4.ipify.org` / `api6.ipify.org`，失败时回退 `icanhazip.com`。入口 IP 只用于反查 ASN 网段，不会外传。

---

## 手动部署

```bash
git clone https://github.com/G1oow/reality-checker-main.git /root/rc-new
cd /root/rc-new
chmod +x reality-checker-main
./reality-checker-main auto <你的VPS_IP> --limit 5
```

Windows 直接双击 `reality-checker-main.exe` 或按上面 PowerShell 命令运行。

**注意：`data/` 目录必须与主程序同级**，否则启动时会尝试从 GitHub 拉规则库，网络不好就会失败。

---

## 源码与自编译

完整源码在 [`src/`](./src)，取自上游 [qualvey/one-reality](https://github.com/qualvey/one-reality) 的 main 分支 —— 即修复了 `v2.2.3` csv 解析 bug 与 `v1.1.2` 证书校验 bug 的那条分支。Go + React 双端。

```bash
cd src
./build.sh          # Linux，产物输出到 src/dist/
```

```powershell
cd src
.\build.ps1         # Windows
```
构建脚本会自动校验（缺失则下载）`internal/data/assets/Country.mmdb` 用于内置 GeoIP，国内网络先在 `src/config.yaml` 配好 `proxy` 再构建。产物默认名为 `reality-checker`，需要与其他脚本对齐时用 `APP_NAME=reality-checker-main` 覆盖；改完源码把产物覆盖到仓库根目录即可。构建不依赖源码目录下的 `src/data/`；运行时若需外部规则库，直接读主程序同级的 `data/`。
构建脚本会自动校验（缺失则下载）`internal/data/assets/Country.mmdb` 用于内置 GeoIP，国内网络先在 `src/config.yaml` 配好 `proxy` 再构建。产物默认名为 `reality-checker`，需要与其他脚本对齐时用 `APP_NAME=reality-checker-main` 覆盖；改完源码把产物覆盖到仓库根目录即可。

**顺序要求**：`web/dist` 是 `go:embed` 的编译依赖，必须先构建前端再编译 Go，否则报 `pattern dist/*: no matching files found`。环境：Go 1.26+、pnpm。

---

## 常用命令

```bash
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

完整参数说明见下方「常用命令」与 `reality-checker-main --help`，结果列含义见「挑选建议」。

---

## 挑选建议

只看 **推荐星级** 列：能拿到 5 星就用 5 星；同星级选手握时间最低的；实际配置前把前 2~3 个都试连一次。

星级门槛、延迟上限、证书最低天数、CDN/热门过滤、代理等全部在 `config.yaml` 里，改完立即生效，无需重编译。

---

## 常见问题

| 现象 | 处理 |
|---|---|
| `Permission denied` | `chmod +x reality-checker-main`（每次 `scp` 后都要重做） |
| 结果恒为 0 | 先 `./reality-checker-main check <已知可用的域名>` 自检；目标机房全是 CDN 时加 `--allow-cdn` |
| 扫描很慢 | 降低 `--timeout`，1 核机器并发别超过 200 |
| 程序卡死 | 另开终端 `kill -QUIT $(pidof reality-checker-main)` 抓协程堆栈，然后调低 `--threads` |
| 规则库下载失败 | 确认 `data/` 五个文件都在，或在 `config.yaml` 的 `proxy.download` 走代理 |

---

## 文件说明

| 文件 | 说明 |
|---|---|
| `reality-checker-main` | Linux 主程序（自编译版） |
| `reality-checker-main.exe` | Windows 主程序（自编译版） |
| `config.yaml` | 过滤策略与运行参数 |
| `data/` | 离线规则库（必须与主程序同级） |
| `file.csv` | 历史扫描结果样例（已脱敏，IP/域名均为示例值），可直接 `pipe` 喂入 |
| `install.sh` / `install.ps1` | 一键部署脚本 |
| `vps-init.sh` | 新 VPS 开荒脚本（公钥/改端口/测速/调优/选目标/建节点，分段幂等） |
| `src/` | 完整源码（Go + React，含 build.sh / build.ps1） |

---

**免责声明**：本工具仅用于网络技术研究与学习，请遵守当地法律法规，合理使用网络资源。
