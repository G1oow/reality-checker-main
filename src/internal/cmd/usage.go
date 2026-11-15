package cmd

import (
	"flag"
	"fmt"

	"RealityChecker/internal/logger"
	"RealityChecker/internal/version"
)

// PrintBanner 打印单行版本横幅（保持简洁，避免刷屏与错位）
func PrintBanner() {
	logger.Block(fmt.Sprintf("RealityChecker %s · REALITY 伪装目标扫描器", version.GetVersion()))
}

// PrintCommandUsage 打印单个命令的用法（选项表由 flag 包生成，统一输出到 stderr）
func PrintCommandUsage(name, summary string, flags *flag.FlagSet) {
	logger.Block(
		fmt.Sprintf("用法: %s %s", version.GetAppName(), name),
		fmt.Sprintf("说明: %s", summary),
		"",
		"选项:",
	)
	if flags != nil {
		flags.SetOutput(logger.Writer())
		flags.PrintDefaults()
	}
}

// PrintUsage 打印使用说明（无时间戳的对齐块）
func PrintUsage() {
	appName := version.GetAppName()
	logger.Block(
		"用法:",
		fmt.Sprintf("  %s ui                        进入 VPS 终端面板（推荐，快捷指令 rcm）", appName),
		fmt.Sprintf("  %s get <ip> [-n 5] [-s 3]   以指定 IP 极速获取优质伪装目标（缓存优先 + 流式现扫）", appName),
		fmt.Sprintf("  %s auto <ip/cidr> [-v]      按入口 IP 的 ASN 自动拉取网段并并发扫描", appName),
		fmt.Sprintf("  %s asn <ASN> <国家>         查询 ASN 网段并按国家过滤（支持 --stats/--seed/--sync）", appName),
		fmt.Sprintf("  %s serve [--port 8881]      启动 Headless REST/SSE 服务（供 WebUI 与外部面板调用）", appName),
		fmt.Sprintf("  %s daemon [--threads 500]   启动全网 ASN 自动化扫描守护进程", appName),
		fmt.Sprintf("  %s patrol [--daemon]        对资产库历史目标执行健康复核与生命周期维护", appName),
		fmt.Sprintf("  %s pipe                     从标准输入流式读取 RealiTLScanner 结果并扫描", appName),
		fmt.Sprintf("  %s check <domain>           扫描单个域名", appName),
		fmt.Sprintf("  %s batch <d1> <d2> ...      批量扫描多个域名", appName),
		fmt.Sprintf("  %s version                  显示版本信息", appName),
		"",
		"全局选项:",
		"  -c, --config FILE           指定配置文件路径（默认自动探测）",
		"  -f, --force                 跳过出口 IP 探测失败时的阻断，强制启动",
		"",
		"示例:",
		fmt.Sprintf("  %s ui --mode country --country DE -m 3", appName),
		fmt.Sprintf("  %s ui --family v6 --limit 1", appName),
		fmt.Sprintf("  %s get 198.51.100.10 -n 5 -s 4", appName),
		fmt.Sprintf("  %s asn AS15169 US > asn.txt", appName),
		fmt.Sprintf("  cat file.csv | %s pipe", appName),
	)
}
