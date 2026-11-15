package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"RealityChecker/internal/app"
	asnclient "RealityChecker/internal/asn"
	"RealityChecker/internal/config"
	"RealityChecker/internal/core"
	"RealityChecker/internal/logger"
	"RealityChecker/internal/panel"
	"RealityChecker/internal/types"
	"RealityChecker/internal/version"

	"golang.org/x/term"
)

// PrintUIPanelUsage 打印面板用法（无时间戳对齐块）
func PrintUIPanelUsage() {
	appName := version.GetAppName()
	logger.Block(
		"用法:",
		fmt.Sprintf("  %s ui [选项]             进入 VPS 终端面板（推荐，快捷指令 rcm）", appName),
		"",
		"常用选项:",
		"  --mode entry|country     扫描模式：入口 IP 的 ASN / 指定国家地区的 ASN（默认沿用上次设置）",
		"  --country CODE           国家/地区 ISO 代码，如 DE、JP（配合 --mode country）",
		"  --family v4|v6|both      入口协议族（默认沿用上次设置）",
		"  -m, --limit N            命中目标数量上限 1~100（默认沿用上次设置）",
		"  --threads N              扫描并发（10~2000）",
		"  --timeout N              单 IP 超时秒数（1~15）",
		"  --settings FILE          面板设置文件路径",
		"  --no-tui                 不启动全屏面板，直接以纯文本跑一次扫描（脚本/无终端环境）",
		"  -h, --help               显示本帮助",
		"",
		"面板快捷键:",
		"  s 开始扫描 · x/esc 停止 · r 重新探测入口 · ↑↓ 选择 · ←→ 改值 · 空格切换开关 · tab 切换焦点 · q 退出",
		"",
		"示例:",
		fmt.Sprintf("  %s ui                              进入面板，默认按 VPS 入口 IP 的 ASN 扫描", appName),
		fmt.Sprintf("  %s ui --mode country --country DE -m 3", appName),
		fmt.Sprintf("  %s ui --family v6 --limit 1       仅用 IPv6 入口，命中 1 个目标即停", appName),
	)
}

// executeUI 启动 VPS 终端面板
func (r *RootCmd) executeUI(args []string) {
	flags := flag.NewFlagSet("ui", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() { PrintUIPanelUsage() }

	modeFlag := flags.String("mode", "", "扫描模式: entry 或 country")
	countryFlag := flags.String("country", "", "国家/地区 ISO 代码")
	familyFlag := flags.String("family", "", "入口协议族: v4 / v6 / both")
	limitFlag := flags.Int("limit", 0, "命中目标数量上限")
	flags.IntVar(limitFlag, "m", 0, "命中目标数量上限 (缩写)")
	threadsFlag := flags.Int("threads", 0, "扫描并发")
	timeoutFlag := flags.Int("timeout", 0, "单 IP 超时秒数")
	settingsFlag := flags.String("settings", "", "面板设置文件路径")
	noTUIFlag := flags.Bool("no-tui", false, "不启动全屏面板，直接以纯文本跑一次扫描")
	helpFlag := flags.Bool("help", false, "显示帮助")
	flags.BoolVar(helpFlag, "h", false, "显示帮助 (缩写)")

	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			PrintUIPanelUsage()
		}
		return
	}
	if *helpFlag {
		PrintUIPanelUsage()
		return
	}

	baseCfg := types.Config{}
	if r.config != nil {
		baseCfg = *r.config
	} else if loaded, err := config.LoadConfig(""); err == nil && loaded != nil {
		baseCfg = *loaded
	}

	settingsPath := strings.TrimSpace(*settingsFlag)
	if settingsPath == "" {
		settingsPath = panel.DefaultSettingsPath()
	}
	settings, err := panel.LoadSettings(settingsPath, baseCfg.Filter)
	if err != nil {
		logger.Linef(logger.IconWarn, "读取设置", "失败，已使用默认参数（%s）", err.Error())
	}

	// 命令行参数覆盖持久化设置
	if *modeFlag != "" {
		switch strings.ToLower(strings.TrimSpace(*modeFlag)) {
		case "country":
			settings.Mode = panel.ModeCountry
		case "entry":
			settings.Mode = panel.ModeEntry
		default:
			logger.Linef(logger.IconError, "参数错误", "--mode 只支持 entry 或 country")
			return
		}
	}
	if *countryFlag != "" {
		settings.Country = strings.ToUpper(strings.TrimSpace(*countryFlag))
	}
	if *familyFlag != "" {
		switch strings.ToLower(strings.TrimSpace(*familyFlag)) {
		case "v4", "4", "ipv4":
			settings.Family = panel.FamilyV4
		case "v6", "6", "ipv6":
			settings.Family = panel.FamilyV6
		case "both", "all":
			settings.Family = panel.FamilyBoth
		default:
			logger.Linef(logger.IconError, "参数错误", "--family 只支持 v4、v6 或 both")
			return
		}
	}
	if *limitFlag > 0 {
		settings.Limit = *limitFlag
	}
	if *threadsFlag > 0 {
		settings.Threads = *threadsFlag
	}
	if *timeoutFlag > 0 {
		settings.TimeoutSec = *timeoutFlag
	}
	settings.Normalize()

	if reason := settings.Validate(); reason != "" {
		logger.Linef(logger.IconError, "参数错误", "%s", reason)
		return
	}

	// 构建扫描依赖（面板不接触资产数据库）
	container, err := app.NewContainer(app.ContainerOptions{
		Config:       r.config,
		ExplicitMMDB: "data/Country.mmdb",
	})
	if err != nil {
		logger.Linef(logger.IconError, "初始化失败", "%s", err.Error())
		return
	}
	defer container.Close()

	geoReader := container.GetGeoIP()
	scanEngine := container.GetScanner()
	resolver := container.GetASNClient()

	proxyURL := ""
	if r.config != nil && r.config.Proxy.Download {
		proxyURL = r.config.Proxy.URL
	}
	countryClient := asnclient.NewClientWithProxy(proxyURL, asnclient.CountryResourceTimeout())

	triggerEngine := core.NewEngine(&baseCfg)
	if err := triggerEngine.Start(); err != nil {
		logger.Linef(logger.IconError, "启动引擎", "失败（%s）", err.Error())
		return
	}
	defer triggerEngine.Stop()

	deps := panel.Deps{
		Reader:   geoReader,
		Resolver: resolver,
		Country:  countryClient,
		Engine:   triggerEngine,
		Scanner:  scanEngine,
		BaseCfg:  baseCfg,
	}

	ctx := r.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	if *noTUIFlag || !isInteractiveTerminal() {
		if !*noTUIFlag {
			logger.Linef(logger.IconWarn, "环境提示", "未检测到交互终端，已切换为纯文本扫描模式")
		}
		runHeadlessPanel(ctx, deps, settings, settingsPath, countryClient)
		return
	}

	if err := panel.RunTUI(ctx, deps, settings, settingsPath); err != nil {
		logger.Linef(logger.IconError, "面板退出", "%s", err.Error())
	}
}

// runHeadlessPanel 以纯文本方式跑一次扫描
func runHeadlessPanel(ctx context.Context, deps panel.Deps, settings panel.Settings, settingsPath string, countryClient panel.CountryClient) {
	var plan panel.Plan
	switch settings.Mode {
	case panel.ModeCountry:
		plan = panel.NewCountryPlan(countryClient, settings.Country, settings.Family)
	default:
		probeCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		options := panel.ProbeEntries(probeCtx, deps.Reader, deps.Resolver)
		cancel()
		for _, option := range options {
			if option.Available {
				logger.Linef(logger.IconOK, "入口候选", "%s", option.Summary())
			} else {
				logger.Linef(logger.IconWarn, "入口候选", "%s", option.Summary())
			}
		}
		plan = panel.NewEntryPlan(options, settings.Family)
	}

	runner := panel.NewRunner(panel.RunnerConfig{
		Engine:  deps.Engine,
		Scanner: deps.Scanner,
		Filter:  settings.FilterConfig(deps.BaseCfg.Filter),
		Limit:   settings.Limit,
	})
	_ = settingsPath
	panel.RunHeadless(ctx, settings, plan, runner, 3*time.Second)
}

// isInteractiveTerminal 判断当前是否处于交互式终端
func isInteractiveTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}
