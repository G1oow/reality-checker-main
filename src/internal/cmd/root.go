package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"RealityChecker/internal/batch"
	"RealityChecker/internal/config"
	"RealityChecker/internal/core"
	"RealityChecker/internal/logger"
	"RealityChecker/internal/types"
	"RealityChecker/internal/version"
)

// RootCmd 根命令结构
// EgressInfo 记录本次进程运行时的公网出口归属，用于在扫描 CN 网段时给出视角失真提示
type EgressInfo struct {
	IP          string
	CountryCode string
	IsCN        bool
	Known       bool // 是否成功探测到出口归属
}

// RootCmd 根命令结构
type RootCmd struct {
	config       *types.Config
	engine       *core.Engine
	batchManager *batch.Manager
	ctx          context.Context
	cancel       context.CancelFunc
	egress       *EgressInfo
}

// SetEgressInfo 由 main 在出口探测完成后注入，供各子命令判断测速视角是否可信
func SetEgressInfo(ip, countryCode string, isCN bool) {
	pendingEgress = &EgressInfo{IP: ip, CountryCode: countryCode, IsCN: isCN, Known: ip != ""}
}

// pendingEgress 在 RootCmd 创建前暂存出口信息
var pendingEgress *EgressInfo

// NewRootCmd 创建根命令 (可传入已预解析好的配置，若为空则自动通过默认策略加载)
func NewRootCmd(initialCfg ...*types.Config) (*RootCmd, error) {
	var cfg *types.Config
	if len(initialCfg) > 0 && initialCfg[0] != nil {
		cfg = initialCfg[0]
	} else {
		var err error
		cfg, err = config.LoadConfig("")
		if err != nil {
			return nil, fmt.Errorf("加载配置失败: %v", err)
		}
	}

	// 初始化日志系统
	logger.Init(cfg.Log.Level, cfg.Log.File)

	// 创建引擎
	engine := core.NewEngine(cfg)
	if err := engine.Start(); err != nil {
		return nil, fmt.Errorf("启动引擎失败: %v", err)
	}

	// 创建批量管理器（共享引擎）
	batchManager := batch.NewManagerWithEngine(engine, cfg)
	if err := batchManager.Start(); err != nil {
		engine.Stop()
		return nil, fmt.Errorf("启动批量管理器失败: %v", err)
	}

	// 设置信号处理
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		sigChan := make(chan os.Signal, 2)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		logger.Warn("接收到中断信号（Ctrl+C），正在安全退出")
		cancel()

		// 再次收到中断信号或超过 2 秒未退出时立即强制终止
		select {
		case <-sigChan:
			logger.Warn("强制终止进程")
			os.Exit(130)
		case <-time.After(2 * time.Second):
			os.Exit(130)
		}
	}()

	return &RootCmd{
		config:       cfg,
		engine:       engine,
		batchManager: batchManager,
		ctx:          ctx,
		cancel:       cancel,
		egress:       pendingEgress,
	}, nil
}

// Execute 执行命令
func (r *RootCmd) Execute(customArgs ...[]string) {
	defer r.cleanup()

	var args []string
	if len(customArgs) > 0 && len(customArgs[0]) > 0 {
		args = customArgs[0]
	} else if len(os.Args) >= 2 {
		args = os.Args[1:]
	} else {
		PrintUsage()
		os.Exit(1)
	}

	globalVerbose := false
	if len(args) > 1 && (args[0] == "-v" || args[0] == "--verbose") {
		globalVerbose = true
		args = args[1:]
	}

	subCmd := args[0]
	subArgs := args[1:]
	if globalVerbose {
		subArgs = append(subArgs, "-v")
	}

	switch subCmd {
	case "ui":
		r.executeUI(subArgs)
	case "serve":
		r.executeServe(subArgs)
	case "daemon":
		r.executeDaemon(subArgs)
	case "patrol":
		r.executePatrol(subArgs)
	case "get":
		r.executeGet(subArgs)
	case "asn":
		executeASN(subArgs)
	case "pipe":
		r.executePipe()
	case "auto":
		r.parseAndExecuteAuto(subArgs)
	case "migrate":
		r.executeMigrate(subArgs)
	case "check":
		if len(subArgs) < 1 {
			logger.Error("缺少域名参数")
			logger.Detail("用法：%s check <domain>", version.GetAppName())
			logger.Detail("示例：%s check apple.com", version.GetAppName())
			os.Exit(1)
		}
		r.executeCheck(subArgs[0])
	case "batch":
		if len(subArgs) < 1 {
			logger.Error("缺少域名参数")
			logger.Detail("用法：%s batch <domain1> <domain2> ...", version.GetAppName())
			logger.Detail("示例：%s batch apple.com google.com microsoft.com", version.GetAppName())
			os.Exit(1)
		}
		domainsStr := strings.Join(subArgs, " ")
		r.executeBatch(domainsStr)
	case "csv":
		logger.Error("'csv' 命令已废弃")
		logger.Detail("请改用流式管道命令：cat file.csv | %s pipe", version.GetAppName())
		logger.Detail("或使用全自动扫描命令：%s auto <ip/cidr>", version.GetAppName())
		os.Exit(1)
	case "version", "-v", "--version":
		r.showVersion()
	case "help", "-h", "--help":
		PrintUsage()
	default:
		logger.Error("未知命令：%s", subCmd)
		logger.Detail("可用命令：ui、serve、daemon、patrol、auto、asn、migrate、pipe、check、batch、version")
		os.Exit(1)
	}
}

// showVersion 显示版本信息
func (r *RootCmd) showVersion() {
	logger.Block(
		"RealityChecker · REALITY 伪装目标扫描器",
		fmt.Sprintf("版本 (version)：%s", version.GetVersion()),
		fmt.Sprintf("提交 (commit)：%s", version.GetCommit()),
		fmt.Sprintf("构建时间 (build)：%s", version.GetBuildTime()),
	)
}

// cleanup 清理资源
func (r *RootCmd) cleanup() {
	if r.batchManager != nil {
		r.batchManager.Stop()
	}
	if r.engine != nil {
		r.engine.Stop()
	}
	if r.cancel != nil {
		r.cancel()
	}
}
