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

	"RealityChecker/internal/app"
	"RealityChecker/internal/config"
	"RealityChecker/internal/logger"
	"RealityChecker/internal/server"
	"RealityChecker/internal/version"
)

func (r *RootCmd) executeServe(args []string) {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		PrintCommandUsage("serve [选项]", "启动 Headless REST/SSE 服务（供 Web 控制台与外部面板调用）", flags)
	}

	portFlag := flags.Int("port", 8881, "HTTP 服务监听端口")
	hostFlag := flags.String("host", "0.0.0.0", "HTTP 服务监听地址")
	dbFlag := flags.String("db", "data/reality_targets.db", "SQLite 资产数据库文件路径")
	mmdbFlag := flags.String("mmdb", "data/Country.mmdb", "GeoIP 国家数据库文件路径")
	tokenFlag := flags.String("token", "", "API 访问鉴权 Token (留空则不开启鉴权)")

	if err := flags.Parse(args); err != nil {
		return
	}

	addr := fmt.Sprintf("%s:%d", *hostFlag, *portFlag)

	cfg, _ := config.LoadConfig("")

	// 1. 初始化统一应用依赖容器
	container, err := app.NewContainer(app.ContainerOptions{
		Config:       cfg,
		ExplicitDB:   *dbFlag,
		ExplicitMMDB: *mmdbFlag,
	})
	if err != nil {
		logger.Error("容器初始化失败：%v", err)
		return
	}
	defer container.Close()

	if _, err := container.GetStore(); err != nil {
		logger.Warn("资产库初始化失败：%v（改用无缓存模式）", err)
	}

	provService, err := container.GetProvisionService()
	if err != nil {
		logger.Error("供给服务构建失败：%v", err)
		return
	}

	apiToken := *tokenFlag
	if apiToken == "" {
		if envToken := os.Getenv("RC_API_TOKEN"); envToken != "" {
			apiToken = envToken
		} else if cfg != nil && cfg.Server.APIToken != "" {
			apiToken = cfg.Server.APIToken
		}
	}

	srvConfig := server.DefaultServerConfig()
	srvConfig.Addr = addr
	srvConfig.APIToken = apiToken

	targetServer := server.NewTargetServer(srvConfig, provService)

	// 4. 打印启动信息
	logger.Blank()
	lines := []string{
		fmt.Sprintf("%s RealityChecker API 与 Web 控制台启动中 [%s]", logger.IconStep, version.GetVersion()),
		"  " + logger.Alignment("监听地址", 14) + fmt.Sprintf("http://%s", addr),
		"  " + logger.Alignment("Web 控制台", 14) + fmt.Sprintf("http://%s/", addr),
		"  " + logger.Alignment("资产数据库", 14) + *dbFlag,
	}
	if apiToken != "" {
		prefixLen := 4
		if len(apiToken) < 4 {
			prefixLen = len(apiToken)
		}
		lines = append(lines, "  "+logger.Alignment("接口鉴权", 14)+fmt.Sprintf("已启用（Token: %s***）", apiToken[:prefixLen]))
	} else {
		lines = append(lines, "  "+logger.Alignment("接口鉴权", 14)+"未启用（公网暴露建议配置 --token 或在 config.yaml 启用）")
	}
	if container.GetGeoIP() != nil {
		lines = append(lines, "  "+logger.Alignment("GeoIP 数据库", 14)+*mmdbFlag)
	}
	lines = append(lines,
		"  "+logger.Alignment("API 端点", 14),
		"      "+logger.Alignment("智能同 ASN 权重", 18)+fmt.Sprintf("GET http://%s/api/targets/smart?ip=<IP>&limit=10", addr),
		"      "+logger.Alignment("资产全量管理", 18)+fmt.Sprintf("GET http://%s/api/targets?page=1&page_size=20", addr),
		"      "+logger.Alignment("资产概览统计", 18)+fmt.Sprintf("GET http://%s/api/stats", addr),
		"      "+logger.Alignment("SSE 流式扫描", 18)+fmt.Sprintf("GET http://%s/api/targets/stream?ip=<IP>&limit=5", addr),
		"      "+logger.Alignment("服务健康", 18)+fmt.Sprintf("GET http://%s/api/health", addr),
	)
	logger.Block(lines...)
	logger.Blank()
	logger.Step("服务就绪：按 Ctrl+C 优雅退出")

	// 5. 启动服务与优雅停机
	errChan := make(chan error, 1)
	go func() {
		if err := targetServer.Start(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errChan:
		logger.Error("服务异常终止：%v", err)
	case <-sigChan:
		logger.Step("HTTP 服务平滑关闭")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := targetServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("服务强制终止：%v", err)
		} else {
			logger.Success("HTTP 服务已关闭")
		}
	case <-r.ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = targetServer.Shutdown(shutdownCtx)
	}
}
