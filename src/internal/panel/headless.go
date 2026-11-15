package panel

import (
	"context"
	"fmt"
	"time"

	"RealityChecker/internal/logger"

	tea "github.com/charmbracelet/bubbletea"
)

// RunTUI 启动全屏终端面板（阻塞直到用户退出）
func RunTUI(ctx context.Context, deps Deps, settings Settings, settingsPath string) error {
	model := NewModel(ctx, deps, settings, settingsPath)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := program.Run()
	return err
}

// RunHeadless 以纯文本方式执行一次扫描（非交互环境 / 脚本兜底）
//
// 返回值：命中目标列表与最终扫描状态；所有人类可读输出经 logger 写入 stderr。
func RunHeadless(
	ctx context.Context,
	settings Settings,
	plan Plan,
	runner *Runner,
	progressEvery time.Duration,
) ([]Row, Status) {
	if progressEvery <= 0 {
		progressEvery = 3 * time.Second
	}
	settings.Normalize()

	logger.Linef(logger.IconStep, "扫描模式", "%s", modeLabel(settings.Mode))
	if settings.Mode == ModeCountry {
		logger.Linef(logger.IconStep, "国家/地区", "%s", settings.Country)
	}
	logger.Linef(logger.IconStep, "目标数量", "%d（并发 %d，超时 %ds，协议族 %s）",
		settings.Limit, settings.Threads, settings.TimeoutSec, familyLabel(settings.Family))
	logger.Linef(logger.IconStep, "过滤策略", "星级 ≥ %s，握手 ≤ %s，证书 ≥ %s，CDN %s，热门大站 %s，国内站点 %s",
		optionalCount(settings.Filter.MinStars, " 星"),
		optionalMS(settings.Filter.MaxHandshakeMS),
		optionalCount(settings.Filter.MinCertDays, " 天"),
		allowedLabel(settings.Filter.AllowCDN),
		allowedLabel(settings.Filter.AllowHot),
		"排除="+yesNo(settings.Filter.ExcludeCN))

	if err := runner.Start(ctx, plan); err != nil {
		logger.Linef(logger.IconError, "启动失败", "%s", err.Error())
		return nil, runner.Status()
	}

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	lastProgress := time.Now()
	lastPhase := PhaseIdle
	reportedHits := 0

	for {
		status := runner.Status()
		if status.Phase != lastPhase {
			lastPhase = status.Phase
			switch status.Phase {
			case PhasePrepare:
				logger.Linef(logger.IconStep, "准备计划", "%s", status.Message)
			case PhaseScan:
				if status.Plan.Country != "" {
					logger.Linef(logger.IconStep, "国家 ASN 清单", "%s 共 %d 个 ASN（IPv4 网段 %d / IPv6 网段 %d，查询时间 %s）",
						status.Plan.Country, status.Plan.CountryASNs, status.Plan.CountryIPv4, status.Plan.CountryIPv6, status.Plan.CountryQueryTime)
				}
				if status.Plan.Note != "" {
					logger.Linef(logger.IconWarn, "入口提示", "%s", status.Plan.Note)
				}
				logger.Linef(logger.IconStep, "开始扫描", "%s", plan.Summary())
			}
		}

		rows := runner.Rows()
		for _, row := range rows[reportedHits:] {
			logger.Linef(logger.IconOK, "命中目标", "%s（%d 星 / %d ms / 证书 %d 天 / %s / %s）",
				row.Domain, row.Stars, row.HandshakeMS, row.CertDays, emptyAs(row.ASN, "未知 ASN"), emptyAs(row.Country, "未知地区"))
		}
		if len(rows) > reportedHits {
			reportedHits = len(rows)
		}

		if status.Phase == PhaseScan && time.Since(lastProgress) >= progressEvery {
			lastProgress = time.Now()
			logger.Linef(logger.IconDetail, "扫描进度", "已扫描 %s 个 IP · 命中 %d/%d · 速率 %s · 当前网段 %s",
				humanInt(status.ScannedIPs), status.Hits, settings.Limit, humanRate(status.Rate), emptyAs(status.CurrentCIDR, "—"))
		}

		if status.Phase == PhaseDone || status.Phase == PhaseStopped || status.Phase == PhaseFailed {
			reportFinal(logger.IconOK, logger.IconError, status, rows)
			return rows, status
		}

		select {
		case <-ctx.Done():
			runner.Stop()
			runner.Wait(5 * time.Second)
			final := runner.Status()
			logger.Linef(logger.IconWarn, "扫描中断", "已停止（已扫描 %s 个 IP，命中 %d 个目标）",
				humanInt(final.ScannedIPs), len(runner.Rows()))
			return runner.Rows(), final
		case <-ticker.C:
		}
	}
}

// reportFinal 输出扫描终态与结果表
func reportFinal(okIcon, errIcon string, status Status, rows []Row) {
	switch status.Phase {
	case PhaseFailed:
		logger.Linef(errIcon, "扫描失败", "%s：%s", status.Message, status.Reason)
	default:
		logger.Linef(okIcon, "扫描完成", "%s（命中 %d 个目标，已扫描 %s 个 IP，用时 %s）",
			status.Message, len(rows), humanInt(status.ScannedIPs), humanDuration(status.Elapsed))
	}
	if len(rows) == 0 {
		logger.Linef(logger.IconWarn, "结果为空", "本次未命中符合条件的目标，可放宽星级/握手延迟限制后重试")
		return
	}
	logger.Block(fmt.Sprintf("  %s %s %s %s %s %s %s",
		pad("域名", 32), pad("IP", 16), pad("ASN", 12), pad("星级", 5), pad("握手", 8), pad("证书天", 7), "CDN"))
	for _, row := range rows {
		logger.Block(fmt.Sprintf("  %s %s %s %s %s %s %s",
			pad(row.Domain, 32),
			pad(row.IP, 16),
			pad(emptyAs(row.ASN, "—"), 12),
			pad(fmt.Sprintf("%d 星", row.Stars), 5),
			pad(fmt.Sprintf("%d ms", row.HandshakeMS), 8),
			pad(fmt.Sprintf("%d", row.CertDays), 7),
			yesNo(row.IsCDN)))
	}
}

// modeLabel 返回扫描模式显示名
func modeLabel(mode Mode) string {
	if mode == ModeCountry {
		return "按国家/地区 ASN"
	}
	return "按 VPS 入口 IP ASN"
}

// allowedLabel 返回允许类开关的显示名
func allowedLabel(allowed bool) string {
	if allowed {
		return "允许"
	}
	return "排除"
}
