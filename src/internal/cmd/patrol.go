package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"RealityChecker/internal/app"
	"RealityChecker/internal/core"
	"RealityChecker/internal/logger"
	"RealityChecker/internal/types"
)

// executePatrol 启动存量 REALITY 资产健康巡检与生命周期淘汰
func (r *RootCmd) executePatrol(args []string) {
	flags := flag.NewFlagSet("patrol", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		PrintCommandUsage("patrol [选项]", "对资产库历史目标执行健康复核与生命周期维护", flags)
	}

	storePath := flags.String("store", "data/reality_targets.db", "SQLite 目标数据库路径")
	concurrency := flags.Int("concurrency", 30, "巡检并发协程数")
	flags.IntVar(concurrency, "c", 30, "巡检并发协程数 (缩写)")
	timeoutSec := flags.Int("timeout", 3, "单目标 TLS 探活超时秒数")
	isDaemon := flags.Bool("daemon", false, "常驻后台 Watchdog 模式运行")
	intervalMin := flags.Int("interval", 60, "后台 Watchdog 模式下巡检周期分钟数")

	if err := flags.Parse(args); err != nil {
		return
	}

	logger.Step("巡检服务启动：资产保鲜 Watchdog")

	// 1. 初始化统一应用依赖容器
	container, err := app.NewContainer(app.ContainerOptions{
		Config:     r.config,
		ExplicitDB: *storePath,
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

	cfg := core.DefaultPatrolConfig()
	cfg.Concurrency = *concurrency
	cfg.Timeout = time.Duration(*timeoutSec) * time.Second

	events := core.PatrolEvents{
		OnLog: func(msg string) {
			logger.Info("%s", msg)
		},
		OnTargetChecked: func(target *types.TargetRecord, success bool, ms int64, err error) {
			if success {
				logger.Linef(logger.IconOK, "复核通过", "%s 握手 %dms、证书剩余 %d 天", target.Domain, ms, target.CertDays)
			} else {
				logger.Linef(logger.IconError, "复核失败", "%s %v（连续失败 %d 次）", target.Domain, err, target.CheckFailCount+1)
			}
		},
	}

	watchdog := core.NewPatrolWatchdog(store, cfg, events)

	ctx, cancel := context.WithCancel(r.ctx)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigChan:
			logger.Warn("退出信号：巡检守护服务平滑停机")
			cancel()
		case <-ctx.Done():
		}
	}()

	if *isDaemon {
		cycleInterval := time.Duration(*intervalMin) * time.Minute
		if err := watchdog.Start(ctx, cycleInterval); err != nil && err != context.Canceled {
			logger.Error("巡检 Watchdog 异常：%v", err)
			return
		}
	} else {
		// 单次巡检并输出汇总报告
		report, err := watchdog.RunOnce(ctx)
		if err != nil {
			logger.Error("巡检执行失败：%v", err)
			return
		}
		logger.Blank()
		logger.Block(
			"存量资产健康巡检汇总报告",
			"  "+logger.Alignment("复核资产总数 (Checked)", 28)+fmt.Sprintf("%d", report.TotalChecked),
			"  "+logger.Alignment("存活有效目标 (Healthy)", 28)+fmt.Sprintf("%d", report.Successful),
			"  "+logger.Alignment("异常或失效目标 (Unhealthy)", 28)+fmt.Sprintf("%d", report.Failed),
			"  "+logger.Alignment("超期日志清理 (Cleaned Logs)", 28)+fmt.Sprintf("%d 条", report.CleanedLogs),
			"  "+logger.Alignment("失效资产清理 (Purged)", 28)+fmt.Sprintf("%d 个", report.CleanedTargets),
			"  "+logger.Alignment("总巡检耗时 (Duration)", 28)+report.Duration.Round(time.Millisecond).String(),
		)
	}
}
