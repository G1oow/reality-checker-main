package cmd

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"RealityChecker/internal/adaptive"
	"RealityChecker/internal/app"
	"RealityChecker/internal/core"
	"RealityChecker/internal/logger"
	"RealityChecker/internal/scanner"
	"RealityChecker/internal/server"
	"RealityChecker/internal/types"
)

// executeDaemon 启动 24 小时全网 ASN 自动化扫描守护进程
func (r *RootCmd) executeDaemon(args []string) {
	flags := flag.NewFlagSet("daemon", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		PrintCommandUsage("daemon [选项]", "启动全网 ASN 自动扫描守护进程（可选内置 HTTP 服务）", flags)
	}

	storePath := flags.String("store", "data/reality_targets.db", "SQLite 目标与 ASN 数据库路径")
	dbPath := flags.String("db", "data/Country.mmdb", "GeoIP 国家数据库路径")
	threads := flags.Int("threads", 500, "扫描并发协程数")
	flags.IntVar(threads, "t", 500, "扫描并发协程数 (缩写)")
	timeoutSec := flags.Int("timeout", 3, "单 IP 探测超时秒数")
	singleASN := flags.Int("asn", 0, "仅持续扫描指定的单个 ASN (如 13335)")
	countryFilter := flags.String("country", "", "限定国家代码 (例如 US, JP, HK, DE)")
	ipv4Only := flags.Bool("ipv4-only", true, "仅扫描 IPv4 网段")
	flags.BoolVar(ipv4Only, "4", true, "仅扫描 IPv4 网段 (缩写)")
	ipv6Only := flags.Bool("ipv6-only", false, "仅扫描 IPv6 网段")
	flags.BoolVar(ipv6Only, "6", false, "仅扫描 IPv6 网段 (缩写)")
	highRatio := flags.Float64("high-ratio", 0.70, "热门高优池调度倾斜比率 (0.0~1.0)")
	cycleIntervalSec := flags.Int("interval", 10, "每轮扫描完成后的等待间隔秒数")
	portFlag := flags.Int("port", 8881, "内置 HTTP API 与 Web 控制台监听端口 (0 则禁用 HTTP)")
	hostFlag := flags.String("host", "0.0.0.0", "内置 HTTP 服务监听地址")
	tokenFlag := flags.String("token", "", "API 访问鉴权 Token (留空则不开启鉴权)")
	noHTTP := flags.Bool("no-http", false, "禁用内置 HTTP API 与 Web 控制台")
	verbose := flags.Bool("verbose", false, "详细模式，输出每个网段每个 IP 的实时扫描明细到 stdout")
	flags.BoolVar(verbose, "v", false, "详细模式 (缩写)")

	if err := flags.Parse(args); err != nil {
		return
	}

	logger.Step("守护进程启动：24 小时 ASN 全网扫描")
	if r.config == nil || r.config.LoadedConfigFile == "" {
		logger.Info("配置：未加载配置文件，使用内置默认值")
	} else {
		logger.Linef(logger.IconStep, "配置", "已加载 %s", r.config.LoadedConfigFile)
	}

	// 1. 初始化统一应用依赖容器
	container, err := app.NewContainer(app.ContainerOptions{
		Config:       r.config,
		ExplicitDB:   *storePath,
		ExplicitMMDB: *dbPath,
	})
	if err != nil {
		logger.Error("容器初始化失败：%v", err)
		return
	}
	defer container.Close()

	store, err := container.GetStore()
	if err != nil {
		logger.Error("资产库打开失败：%v", err)
		return
	}
	logger.Linef(logger.IconDetail, "资产库", "后端 %T、目标 %d 个、扫描代理 %t", store, store.Count(), r.config != nil && r.config.Proxy.Scanner)
	geoipReader := container.GetGeoIP()
	scannerEngine := container.GetScanner()

	// 4. 启动内置 HTTP API 与 Web 控制台 (默认随守护进程一同启动)
	var targetServer *server.TargetServer
	if !*noHTTP && *portFlag > 0 {
		provService, err := container.GetProvisionService()
		if err != nil {
			logger.Error("供给服务初始化失败：%v", err)
		} else {
			apiToken := *tokenFlag
			if apiToken == "" {
				if envToken := os.Getenv("RC_API_TOKEN"); envToken != "" {
					apiToken = envToken
				} else if r.config != nil && r.config.Server.APIToken != "" {
					apiToken = r.config.Server.APIToken
				}
			}

			httpAddr := fmt.Sprintf("%s:%d", *hostFlag, *portFlag)
			srvConfig := server.DefaultServerConfig()
			srvConfig.Addr = httpAddr
			srvConfig.APIToken = apiToken

			targetServer = server.NewTargetServer(srvConfig, provService)

			go func() {
				tokenDesc := "未开启"
				if apiToken != "" {
					tokenDesc = "已启用"
				}
				logger.Linef(logger.IconOK, "Web 控制台", "已启动于 http://%s（Token 鉴权: %s）", httpAddr, tokenDesc)
				if err := targetServer.Start(); err != nil && err != http.ErrServerClosed {
					logger.Error("HTTP 服务异常退出：%v", err)
				}
			}()
		}
	}

	// 5. 构建配置与事件回调
	cfg := core.DaemonConfig{
		StorePath:         *storePath,
		CountryDBPath:     *dbPath,
		ScanThreads:       *threads,
		Timeout:           time.Duration(*timeoutSec) * time.Second,
		IPv4Only:          *ipv4Only,
		IPv6Only:          *ipv6Only,
		HighPriorityRatio: *highRatio,
		CycleInterval:     time.Duration(*cycleIntervalSec) * time.Second,
		SingleASN:         *singleASN,
		CountryFilter:     *countryFilter,
		Continuous:        true,
		Verbose:           *verbose,
	}

	events := core.DaemonEvents{
		OnLog: func(msg string) {
			logger.Info("%s", msg)
		},
		OnASNStart: func(asn *types.ASNRecord, cidrCount int, totalIPs int64) {
			logger.Linef(logger.IconStep, "调度选中", "AS%d %s（%s），网段 %d 个、约 %d 个 IP", asn.ASN, asn.Name, asn.Country, cidrCount, totalIPs)
		},
		OnASNComplete: func(asn *types.ASNRecord, foundTargets int, duration time.Duration) {
			logger.Linef(logger.IconOK, "扫描完成", "AS%d 新增目标 %d 个（耗时 %v）", asn.ASN, foundTargets, duration.Round(time.Second))
		},
		OnScanRecord: func(probe *scanner.ProbeResult) {
			if !*verbose || probe == nil {
				return
			}
			if !probe.Success {
				logger.Linef(logger.IconDetail, "扫描失败", "%s:%d（%s）", probe.IP, probe.Port, probe.FailureReason)
			} else {
				logger.Linef(logger.IconOK, "扫描成功", "%s:%d 提取域名 %s（ALPN: %s，套件: %s）",
					probe.IP, probe.Port, probe.Result.CertDomain, probe.Result.ALPN, probe.Result.Curve)
			}
		},
		OnCandidateRejected: func(ip string, domain string, reason string) {
			if !*verbose {
				return
			}
			logger.Linef(logger.IconDetail, "域名未入选", "%s %s（原因: %s）", ip, domain, reason)
		},
		OnCandidateDiscovered: func(ip string, domain string) {
			logger.Linef(logger.IconDetail, "候选域名", "%s（IP: %s）", domain, ip)
		},
		OnAdaptiveAdjust: func(stats adaptive.LimiterStats) {
			logger.Info("自适应调谐：%s", stats.LastActionReason)
		},
		OnTargetFound: func(target *types.TargetRecord) {
			logger.Linef(logger.IconOK, "命中目标", "%s（IP: %s，握手: %dms，评级: %d 星）", target.Domain, target.IP, target.HandshakeMS, target.Stars)
		},
	}

	daemon := core.NewDaemonScheduler(
		r.engine,
		store,
		scannerEngine,
		geoipReader,
		cfg,
		events,
	)

	// 6. 监听平滑退出信号
	ctx, cancel := context.WithCancel(r.ctx)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	stopHTTP := func() {
		if targetServer != nil {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer shutdownCancel()
			_ = targetServer.Shutdown(shutdownCtx)
		}
	}

	go func() {
		select {
		case sig := <-sigChan:
			logger.Warn("退出信号：%s，调度引擎与 HTTP 服务平滑停机", sig)
			stopHTTP()
			cancel()
		case <-ctx.Done():
			stopHTTP()
		}
	}()

	// 7. 运行守护循环
	if err := daemon.Start(ctx); err != nil && err != context.Canceled {
		stopHTTP()
		logger.Error("守护进程异常终止：%v", err)
		return
	}

	stopHTTP()
	logger.Success("守护进程已退出：扫描进度与断点均已保存")
}
