package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"RealityChecker/internal/cmd"
	"RealityChecker/internal/config"
	"RealityChecker/internal/data"
	"RealityChecker/internal/logger"
	"RealityChecker/internal/network"
	"RealityChecker/internal/version"
)

// parseGlobalFlags 从命令行参数中提取全局选项 (-c/--config 配置文件路径, -f/--force 强制拉起标志)
func parseGlobalFlags(args []string) (string, bool, []string) {
	var configPath string
	force := false
	remaining := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-c" || arg == "--config" {
			if i+1 < len(args) {
				configPath = args[i+1]
				i++
				continue
			}
		} else if strings.HasPrefix(arg, "--config=") {
			configPath = strings.TrimPrefix(arg, "--config=")
			continue
		} else if strings.HasPrefix(arg, "-c=") {
			configPath = strings.TrimPrefix(arg, "-c=")
			continue
		} else if arg == "-f" || arg == "--force" {
			force = true
			continue
		} else if strings.HasPrefix(arg, "--force=") {
			val := strings.TrimPrefix(arg, "--force=")
			force = (val == "true" || val == "1")
			continue
		}
		remaining = append(remaining, arg)
	}
	return configPath, force, remaining
}

func main() {
	configPath, force, remainingArgs := parseGlobalFlags(os.Args[1:])

	// 若无参数或显式请求 help (-h, --help, help)，立刻打印帮助信息并退出
	if len(remainingArgs) < 1 {
		cmd.PrintBanner()
		cmd.PrintUsage()
		os.Exit(0)
	}

	subcmd := remainingArgs[0]
	if subcmd == "-h" || subcmd == "--help" || subcmd == "help" {
		cmd.PrintBanner()
		cmd.PrintUsage()
		os.Exit(0)
	}

	// 判断是否需要打印横幅（面板 / pipe / auto / version 自带输出，不叠横幅）
	shouldPrintBanner := true
	if subcmd == "ui" || subcmd == "pipe" || subcmd == "auto" || subcmd == "version" || subcmd == "-v" || subcmd == "--version" || subcmd == "--quiet" || subcmd == "-q" {
		shouldPrintBanner = false
	}
	if shouldPrintBanner {
		cmd.PrintBanner()
	}

	// 统一加载配置 (优先使用 -c/--config，其次探测环境变量/当前目录/二进制所在目录)
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		logger.Linef(logger.IconWarn, "配置加载", "失败，已使用默认配置（%s）", err.Error())
	}

	// 纯版本信息查询与终端面板命令不需要在此探测出口 IP（面板自行分别探测 IPv4/IPv6）
	isVersionCmd := subcmd == "version" || subcmd == "-v" || subcmd == "--version"
	if !isVersionCmd && subcmd != "ui" {
		checkCtx, checkCancel := context.WithTimeout(context.Background(), 4*time.Second)
		geoReader, _ := data.OpenGeoIPReader("data/Country.mmdb", "Country.mmdb")
		egressInfo, err := network.CheckEgressWithReader(checkCtx, geoReader)
		if geoReader != nil {
			geoReader.Close()
		}
		checkCancel()

		if err != nil {
			if !force {
				logger.PrintErrorWithDetails(
					fmt.Sprintf("出口探测失败：%s", err.Error()),
					"原因：启动时需要校验本机公网出口是否可用，当前网络可能未连接外网或探测服务不可达。",
					"处理：确认网络后重试，或追加 --force / -f 强制启动：",
					fmt.Sprintf("  %s --force %s", version.GetAppName(), strings.Join(remainingArgs, " ")),
				)
				os.Exit(1)
			}
			if shouldPrintBanner {
				logger.Linef(logger.IconWarn, "出口探测", "失败（%s），已按 --force 继续运行", err.Error())
			}
		} else if !egressInfo.IsCN {
			if shouldPrintBanner {
				logger.Linef(logger.IconWarn, "出口 IP", "%s 归属 %s / %s，非中国大陆（CN）网络",
					egressInfo.IP, egressInfo.CountryCode, egressInfo.CountryName)
				logger.Detail("扫描中国大陆网段时，握手延迟不代表国内客户端的真实体验")
			}
			cmd.SetEgressInfo(egressInfo.IP, egressInfo.CountryCode, egressInfo.IsCN)
		} else {
			if shouldPrintBanner {
				logger.Linef(logger.IconOK, "出口 IP", "%s 归属 %s / %s",
					egressInfo.IP, egressInfo.CountryCode, egressInfo.CountryName)
			}
			cmd.SetEgressInfo(egressInfo.IP, egressInfo.CountryCode, true)
		}
	}

	// 检查并下载必要的数据文件 (自动遵循配置中的下载代理配置)
	downloadProxy := ""
	if cfg != nil && cfg.Proxy.Download && cfg.Proxy.URL != "" {
		downloadProxy = cfg.Proxy.URL
	}
	downloader := data.NewDownloader(downloadProxy)
	if err := downloader.EnsureDataFiles(); err != nil {
		logger.Linef(logger.IconWarn, "数据文件", "初始化失败，将继续运行（%s）", err.Error())
	}

	// 创建根命令
	rootCmd, err := cmd.NewRootCmd(cfg)
	if err != nil {
		logger.Linef(logger.IconError, "初始化失败", "%s", err.Error())
		os.Exit(1)
	}

	// 执行命令
	rootCmd.Execute(remainingArgs)
}
