package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"RealityChecker/internal/app"
	asnclient "RealityChecker/internal/asn"
	"RealityChecker/internal/config"
	"RealityChecker/internal/logger"
	"RealityChecker/internal/version"
)

func executeASN(args []string) {
	flags := flag.NewFlagSet("asn", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		PrintCommandUsage("asn <ASN> <国家> [选项]", "查询 ASN 网段并按国家过滤，或管理 ASN 注册表", flags)
	}
	countryFlag := flags.String("country", "", "国家 ISO 代码或名称")
	dbPath := flags.String("db", "data/Country.mmdb", "GeoIP 国家数据库路径")
	storePath := flags.String("store", "data/reality_targets.db", "SQLite 目标与 ASN 数据库路径")
	seedFlag := flags.Bool("seed", false, "初始化并加载预设热门云厂商/IDC ASN 注册表")
	syncURL := flags.String("sync", "", "从远程 URL 同步全量 ASN 列表")
	statsFlag := flags.Bool("stats", false, "查看当前 ASN 任务库统计大盘")
	listFlag := flags.Bool("list", false, "列出当前库中的 ASN 记录")
	statusFilter := flags.String("status", "", "过滤 ASN 状态 (pending/scanning/completed)")
	limitFlag := flags.Int("limit", 50, "列表输出最大数量")
	ipv4Only := flags.Bool("ipv4-only", false, "仅输出 IPv4 CIDR 网段")
	flags.BoolVar(ipv4Only, "4", false, "仅输出 IPv4 CIDR 网段 (缩写)")
	ipv6Only := flags.Bool("ipv6-only", false, "仅输出 IPv6 CIDR 网段")
	flags.BoolVar(ipv6Only, "6", false, "仅输出 IPv6 CIDR 网段 (缩写)")

	if err := flags.Parse(args); err != nil {
		return
	}

	cfg, _ := config.LoadConfig("")
	proxyURL := ""
	if cfg != nil && cfg.Proxy.Download && cfg.Proxy.URL != "" {
		proxyURL = cfg.Proxy.URL
	}

	container, err := app.NewContainer(app.ContainerOptions{
		Config:       cfg,
		ExplicitDB:   *storePath,
		ExplicitMMDB: *dbPath,
	})
	if err != nil {
		logger.Error("容器初始化失败：%v", err)
		return
	}
	defer container.Close()

	// 1. 注册表管理逻辑
	if *seedFlag || *syncURL != "" || *statsFlag || *listFlag {
		store, err := container.GetStore()
		if err != nil {
			logger.Error("资产库打开失败：%v", err)
			return
		}

		mgr := asnclient.NewRegistryManagerWithProxy(store, proxyURL)

		if *seedFlag {
			n, err := mgr.InitDefaultRegistry()
			if err != nil {
				logger.Error("ASN 注册表初始化失败：%v", err)
				return
			}
			logger.Success("ASN 注册表初始化：已载入 %d 个预设公有云与 IDC ASN", n)
			return
		}

		if *syncURL != "" {
			logger.Step("ASN 注册表同步：来源 %s", *syncURL)
			n, err := mgr.SyncFromURL(context.Background(), *syncURL)
			if err != nil {
				logger.Error("ASN 注册表同步失败：%v", err)
				return
			}
			logger.Success("同步完成：新增或更新 %d 个 ASN 记录", n)
			return
		}

		if *statsFlag {
			stats, err := mgr.GetStats()
			if err != nil {
				logger.Error("统计查询失败：%v", err)
				return
			}
			logger.Block(
				"ASN 全网扫描任务大盘",
				"  "+logger.Alignment("自治系统总数 (Total)", 28)+fmt.Sprintf("%d", stats.TotalASNs),
				"  "+logger.Alignment("等待扫描 (Pending)", 28)+fmt.Sprintf("%d", stats.PendingASNs),
				"  "+logger.Alignment("正在扫描 (Scanning)", 28)+fmt.Sprintf("%d", stats.ScanningASNs),
				"  "+logger.Alignment("已完成 (Completed)", 28)+fmt.Sprintf("%d", stats.CompletedASNs),
				"  "+logger.Alignment("高优核心厂商 (Priority>=80)", 28)+fmt.Sprintf("%d", stats.HighPriority),
			)
			return
		}

		if *listFlag {
			list, err := store.ListASNs(*statusFilter, 0, *limitFlag, 0)
			if err != nil {
				logger.Error("ASN 列表查询失败：%v", err)
				return
			}
			logger.Plain("%-8s %-6s %-8s %-12s %s", "ASN", "国家", "权重", "状态", "运营商/名称")
			logger.Plain("%s", strings.Repeat("-", 60))
			for _, item := range list {
				logger.Plain("AS%-6d %-6s %-8d %-12s %s", item.ASN, item.Country, item.Priority, item.ScanStatus, item.Name)
			}
			logger.Plain("共显示 %d 条记录", len(list))
			return
		}
	}

	// 2. 原有的 ASN CIDR 网段查询与国家过滤
	if flags.NArg() < 1 {
		logger.Block(
			"用法:",
			fmt.Sprintf("  1. 网段查询: %s asn [--db Country.mmdb] [-4] [-6] <ASN> <国家>", version.GetAppName()),
			fmt.Sprintf("  2. 注册表管理: %s asn [--store db] [--seed] [--stats] [--list] [--sync <url>]", version.GetAppName()),
		)
		return
	}
	country := *countryFlag
	if country == "" && flags.NArg() >= 2 {
		country = flags.Arg(1)
	}
	if strings.TrimSpace(country) == "" {
		logger.Error("缺少国家参数：需指定两位国家代码，例如 CN、US")
		return
	}

	db := container.GetGeoIP()
	if db == nil {
		logger.Warn("本地 GeoIP 库不可用（%s）：国家过滤将只依赖 RIPE 在线国家前缀集合", *dbPath)
	}

	asnClient := asnclient.NewClientWithProxy(proxyURL)
	prefixes, err := asnClient.FetchPrefixes(context.Background(), flags.Arg(0))
	if err != nil {
		logger.Error("ASN 查询失败：%v", err)
		return
	}
	resolver := asnclient.NewCountryResolver(asnClient, db)
	filtered, source := resolver.FilterPrefixes(context.Background(), prefixes, country)
	if source == asnclient.CountrySourceNone {
		logger.Error("国家过滤失败：RIPE 国家前缀集合与本地 GeoIP 均无可用归属数据")
		return
	}
	filtered = asnclient.FilterIPVersion(filtered, *ipv4Only, *ipv6Only)
	for _, prefix := range filtered {
		fmt.Println(prefix)
	}
	logger.Linef(logger.IconOK, "网段筛选", "%d/%d 个属于 %s 的 CIDR", len(filtered), len(prefixes), country)
}
