package panel

import (
	"context"
	"errors"
	"testing"
	"time"

	"RealityChecker/internal/core"
	"RealityChecker/internal/scanner"
	"RealityChecker/internal/types"
)

// fakePlan 仅用于验证 Runner 的编排与终止条件，不产生任何网络行为
type fakePlan struct {
	batches      []*Batch
	prepareErr   error
	prepareBlock bool
	nextErr      error
	nextCalls    int
}

func (p *fakePlan) Mode() Mode       { return ModeEntry }
func (p *fakePlan) Summary() string  { return "测试扫描计划" }
func (p *fakePlan) Stats() PlanStats { return PlanStats{QueueName: "测试队列"} }

func (p *fakePlan) Prepare(ctx context.Context) error {
	if p.prepareBlock {
		<-ctx.Done()
		return ctx.Err()
	}
	return p.prepareErr
}

func (p *fakePlan) Next(context.Context) (*Batch, error) {
	p.nextCalls++
	if p.nextErr != nil {
		return nil, p.nextErr
	}
	if len(p.batches) == 0 {
		return nil, nil
	}
	batch := p.batches[0]
	p.batches = p.batches[1:]
	return batch, nil
}

func newTestRunner(limit int) *Runner {
	return NewRunner(RunnerConfig{
		Engine:  &core.Engine{},
		Scanner: &scanner.Scanner{},
		Filter:  baseFilter(),
		Limit:   limit,
	})
}

func waitPhase(t *testing.T, runner *Runner, want Phase, timeout time.Duration) Status {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status := runner.Status()
		if status.Phase == want {
			return status
		}
		time.Sleep(20 * time.Millisecond)
	}
	status := runner.Status()
	t.Fatalf("等待阶段 %s 超时，当前阶段 %s（%s）", want, status.Phase, status.Message)
	return status
}

func TestRunnerStopsImmediatelyWhenLimitReached(t *testing.T) {
	runner := newTestRunner(1)
	runner.addRow(Row{Domain: "already-hit.example", Stars: 5})
	runner.done = make(chan struct{})
	runner.startedAt = time.Now()
	plan := &fakePlan{}

	// 直接驱动扫描循环，验证“目标数量达标即停”的判定发生在拉取网段之前
	runner.run(context.Background(), plan)

	status := runner.Status()
	if status.Reason != "limit_hit" {
		t.Fatalf("命中上限时应以 limit_hit 结束，实际 %q", status.Reason)
	}
	if plan.nextCalls != 0 {
		t.Fatalf("命中上限后不应再取新批次，实际取了 %d 次", plan.nextCalls)
	}
	if status.Hits != 1 {
		t.Fatalf("命中数应为 1，实际 %d", status.Hits)
	}
	if status.Phase != PhaseDone {
		t.Fatalf("达标后阶段应为已完成，实际 %s", status.Phase)
	}
}

func TestRunnerCompletesWhenQueueExhausted(t *testing.T) {
	runner := newTestRunner(5)
	if err := runner.Start(context.Background(), &fakePlan{}); err != nil {
		t.Fatalf("启动扫描失败: %v", err)
	}
	status := waitPhase(t, runner, PhaseDone, 3*time.Second)
	if status.Reason != "exhausted" {
		t.Fatalf("队列扫完时应以 exhausted 结束，实际 %q", status.Reason)
	}
	if runner.Running() {
		t.Fatalf("结束后 Running 应为 false")
	}
}

func TestRunnerFailsOnPrepareError(t *testing.T) {
	runner := newTestRunner(5)
	plan := &fakePlan{prepareErr: errors.New("RIPE Stat 返回 HTTP 500")}
	if err := runner.Start(context.Background(), plan); err != nil {
		t.Fatalf("启动扫描失败: %v", err)
	}
	status := waitPhase(t, runner, PhaseFailed, 3*time.Second)
	if status.Reason == "" || status.Message == "" {
		t.Fatalf("失败终态应包含原因与说明：%+v", status)
	}
}

func TestRunnerStopsOnRequest(t *testing.T) {
	runner := newTestRunner(5)
	plan := &fakePlan{prepareBlock: true}
	if err := runner.Start(context.Background(), plan); err != nil {
		t.Fatalf("启动扫描失败: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	runner.Stop()
	status := waitPhase(t, runner, PhaseStopped, 3*time.Second)
	if status.Reason != "stopped" {
		t.Fatalf("主动停止应以 stopped 结束，实际 %q", status.Reason)
	}
	if !runner.Wait(time.Second) {
		t.Fatalf("停止后应能在 1 秒内退出")
	}
}

func TestRunnerRejectsMissingDependencies(t *testing.T) {
	runner := NewRunner(RunnerConfig{Limit: 1})
	if err := runner.Start(context.Background(), &fakePlan{}); err == nil {
		t.Fatalf("缺少扫描引擎时应拒绝启动")
	}
	runner = newTestRunner(1)
	if err := runner.Start(context.Background(), nil); err == nil {
		t.Fatalf("扫描计划为空时应拒绝启动")
	}
}

func TestRunnerRowsDeduplicateAndSort(t *testing.T) {
	runner := newTestRunner(5)
	runner.addRow(Row{Domain: "a.example", Stars: 1, HandshakeMS: 300})
	runner.addRow(Row{Domain: "B.example", Stars: 5, HandshakeMS: 120})
	runner.addRow(Row{Domain: "a.example", Stars: 5, HandshakeMS: 10})
	runner.addRow(Row{Domain: "  "})

	rows := runner.Rows()
	if len(rows) != 2 {
		t.Fatalf("重复域名与空域名应被丢弃，实际 %d 条", len(rows))
	}
	if rows[0].Domain != "B.example" {
		t.Fatalf("结果应按星级倒序，实际首条 %s", rows[0].Domain)
	}
	if rows[1].Stars != 1 {
		t.Fatalf("低星应排在后面，实际 %+v", rows[1])
	}
}

func TestRowFromResultMapsFields(t *testing.T) {
	result := &types.DetectionResult{
		Domain:      "target.example",
		Suitable:    true,
		TLS:         &types.TLSResult{SupportsTLS13: true, SupportsX25519: true, SupportsHTTP2: true, HandshakeTime: 120 * time.Millisecond},
		SNI:         &types.SNIResult{SNIMatch: true},
		Certificate: &types.CertificateResult{Valid: true, DaysUntilExpiry: 88},
		CDN:         &types.CDNResult{IsCDN: true, IsHotWebsite: false},
		Network:     &types.NetworkResult{StatusCode: 200, PageTitle: "Example", HandshakeTime: 120 * time.Millisecond},
		Location:    &types.LocationResult{Country: "DE"},
	}
	batch := &Batch{Label: "AS64500", Country: "DE"}
	row := rowFromResult(result, "198.51.100.7", batch, 120, 200)

	if row.Domain != "target.example" || row.IP != "198.51.100.7" {
		t.Fatalf("域名/IP 映射错误: %+v", row)
	}
	if row.ASN != "AS64500" || row.Country != "DE" {
		t.Fatalf("ASN/国家映射错误: %+v", row)
	}
	if row.CertDays != 88 || row.StatusCode != 200 || !row.IsCDN {
		t.Fatalf("证书/状态码/CDN 映射错误: %+v", row)
	}
	if row.Stars < 4 {
		t.Fatalf("TLS1.3 + H2 + SNI 匹配 + 非热门站应得较高星级，实际 %d", row.Stars)
	}

	if nilRow := rowFromResult(nil, "198.51.100.8", nil, 0, 0); nilRow.Domain != "" || nilRow.Country != "" {
		t.Fatalf("nil 结果不应 panic 或产生脏字段: %+v", nilRow)
	}
}

func TestStatusProgress(t *testing.T) {
	unknown := Status{}
	if unknown.Progress() != -1 {
		t.Fatalf("批次未知时进度应为 -1，实际 %v", unknown.Progress())
	}
	status := Status{BatchTotal: 4, BatchIndex: 2}
	if got := status.Progress(); got != 25 {
		t.Fatalf("进度应为 25%%，实际 %v", got)
	}
	withCIDR := Status{BatchTotal: 2, BatchIndex: 1, CIDRIndex: 1, CIDRTotal: 2}
	if got := withCIDR.Progress(); got != 25 {
		t.Fatalf("含网段细分的进度应为 25%%，实际 %v", got)
	}
}

func TestDisplayHelpers(t *testing.T) {
	if got := humanInt(1234567); got != "1,234,567" {
		t.Fatalf("千分位格式错误: %s", got)
	}
	if got := humanInt(999); got != "999" {
		t.Fatalf("三位数不应加分隔符: %s", got)
	}
	if got := humanRate(1500); got != "1.5k IP/s" {
		t.Fatalf("速率格式错误: %s", got)
	}
	if got := humanRate(0); got != "0 IP/s" {
		t.Fatalf("零速率格式错误: %s", got)
	}
	if got := humanDuration(75 * time.Second); got != "1m15s" {
		t.Fatalf("耗时格式错误: %s", got)
	}
	if got := StripANSI("\x1b[1m面板\x1b[0m"); got != "面板" {
		t.Fatalf("ANSI 清理失败: %q", got)
	}
}
