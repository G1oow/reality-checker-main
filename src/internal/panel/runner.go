package panel

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"RealityChecker/internal/core"
	"RealityChecker/internal/scanner"
	"RealityChecker/internal/types"
)

// Phase 扫描阶段
type Phase string

const (
	// PhaseIdle 空闲
	PhaseIdle Phase = "idle"
	// PhasePrepare 正在准备扫描计划
	PhasePrepare Phase = "prepare"
	// PhaseScan 正在扫描
	PhaseScan Phase = "scan"
	// PhaseDone 扫描完成
	PhaseDone Phase = "done"
	// PhaseStopped 已被用户停止
	PhaseStopped Phase = "stopped"
	// PhaseFailed 扫描失败
	PhaseFailed Phase = "failed"
)

// Label 返回阶段的中文名
func (p Phase) Label() string {
	switch p {
	case PhasePrepare:
		return "准备中"
	case PhaseScan:
		return "扫描中"
	case PhaseDone:
		return "已完成"
	case PhaseStopped:
		return "已停止"
	case PhaseFailed:
		return "已失败"
	default:
		return "空闲"
	}
}

// Row 一条命中目标（仅在面板内展示，不落库）
type Row struct {
	Domain      string
	IP          string
	ASN         string
	Country     string
	Stars       int
	HandshakeMS int64
	CertDays    int
	IsCDN       bool
	IsHot       bool
	StatusCode  int
	Title       string
	FoundAt     time.Time
}

// Status 扫描实时状态快照
type Status struct {
	Phase       Phase
	Message     string
	Reason      string
	ScannedIPs  int64
	Hits        int
	Candidates  int
	Rate        float64
	CurrentCIDR string
	CurrentIP   string
	BatchLabel  string
	BatchIndex  int
	BatchTotal  int
	CIDRIndex   int
	CIDRTotal   int
	Elapsed     time.Duration
	Plan        PlanStats
	Err         string
}

// Progress 返回扫描进度百分比（0~100，-1 表示未知）
func (s Status) Progress() float64 {
	if s.BatchTotal <= 0 {
		return -1
	}
	done := float64(s.BatchIndex-1) / float64(s.BatchTotal)
	if s.CIDRTotal > 0 && s.CIDRIndex > 0 {
		done += (float64(s.CIDRIndex) / float64(s.CIDRTotal)) / float64(s.BatchTotal)
	}
	if done < 0 {
		done = 0
	}
	if done > 1 {
		done = 1
	}
	return done * 100
}

// RunnerConfig 扫描运行器依赖
type RunnerConfig struct {
	Engine  *core.Engine
	Scanner *scanner.Scanner
	Filter  types.FilterConfig
	Limit   int
}

// Runner 在内存中执行扫描并汇总结果（不读写资产数据库）
type Runner struct {
	cfg RunnerConfig

	mu        sync.Mutex
	status    Status
	rows      []Row
	seen      map[string]bool
	scannedIP int64
	cancel    context.CancelFunc
	done      chan struct{}
	running   bool
	startedAt time.Time
}

// NewRunner 创建扫描运行器
func NewRunner(cfg RunnerConfig) *Runner {
	if cfg.Limit <= 0 {
		cfg.Limit = 5
	}
	return &Runner{
		cfg:    cfg,
		rows:   make([]Row, 0, 16),
		seen:   make(map[string]bool),
		status: Status{Phase: PhaseIdle, Message: "就绪"},
	}
}

// Start 异步启动扫描
func (r *Runner) Start(parent context.Context, plan Plan) error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return errors.New("扫描正在进行中")
	}
	if r.cfg.Engine == nil || r.cfg.Scanner == nil {
		r.mu.Unlock()
		return errors.New("扫描引擎未初始化")
	}
	if plan == nil {
		r.mu.Unlock()
		return errors.New("扫描计划为空")
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.running = true
	r.rows = make([]Row, 0, 16)
	r.seen = make(map[string]bool)
	r.scannedIP = 0
	r.startedAt = time.Now()
	r.done = make(chan struct{})
	r.status = Status{Phase: PhasePrepare, Message: "正在准备扫描计划"}
	r.mu.Unlock()

	go r.run(ctx, plan)
	return nil
}

// Stop 请求停止扫描
func (r *Runner) Stop() {
	r.mu.Lock()
	cancel := r.cancel
	if r.running {
		r.status.Message = "正在停止扫描"
	}
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Running 返回是否仍在扫描
func (r *Runner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// Wait 等待扫描结束（最多等待 timeout）
func (r *Runner) Wait(timeout time.Duration) bool {
	r.mu.Lock()
	done := r.done
	r.mu.Unlock()
	if done == nil {
		return true
	}
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Status 返回状态快照
func (r *Runner) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := r.status
	snapshot.ScannedIPs = r.scannedIP
	snapshot.Hits = len(r.rows)
	if r.running || !r.startedAt.IsZero() {
		elapsed := time.Since(r.startedAt)
		snapshot.Elapsed = elapsed
		if elapsed > 0 {
			snapshot.Rate = float64(r.scannedIP) / elapsed.Seconds()
		}
	}
	return snapshot
}

// Rows 返回命中目标快照（按星级与握手延迟排序）
func (r *Runner) Rows() []Row {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := make([]Row, len(r.rows))
	copy(rows, r.rows)
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Stars != rows[j].Stars {
			return rows[i].Stars > rows[j].Stars
		}
		return rows[i].HandshakeMS < rows[j].HandshakeMS
	})
	return rows
}

// run 扫描主流程：准备计划 → 逐批扫描 → 命中达标即停
func (r *Runner) run(ctx context.Context, plan Plan) {
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
		close(r.done)
	}()

	if err := plan.Prepare(ctx); err != nil {
		if ctx.Err() != nil {
			r.finish(PhaseStopped, "已停止", "stopped")
			return
		}
		r.finish(PhaseFailed, "计划准备失败", err.Error())
		return
	}
	r.updatePlan(plan.Stats())
	r.setStatus(func(s *Status) {
		s.Phase = PhaseScan
		s.Message = "开始扫描"
	})

	for {
		if ctx.Err() != nil {
			r.finish(PhaseStopped, "已停止", "stopped")
			return
		}
		if r.hitLimit() {
			r.finish(PhaseDone, fmt.Sprintf("命中 %d 个目标，达到目标数量上限", r.cfg.Limit), "limit_hit")
			return
		}

		batch, err := plan.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				r.finish(PhaseStopped, "已停止", "stopped")
				return
			}
			r.finish(PhaseFailed, "拉取网段失败", err.Error())
			return
		}
		if batch == nil {
			r.finish(PhaseDone, "ASN 队列已扫描完毕", "exhausted")
			return
		}
		r.updatePlan(plan.Stats())
		if len(batch.CIDRs) == 0 {
			continue
		}

		remaining := r.cfg.Limit - r.hitCount()
		if remaining <= 0 {
			r.finish(PhaseDone, "命中目标数量达标", "limit_hit")
			return
		}
		r.scanBatch(ctx, batch, remaining)
	}
}

// scanBatch 扫描一批网段
func (r *Runner) scanBatch(ctx context.Context, batch *Batch, maxTargets int) {
	r.setStatus(func(s *Status) {
		s.Phase = PhaseScan
		s.BatchLabel = batch.Label
		s.BatchIndex = s.Plan.BatchesDone
		s.BatchTotal = s.Plan.BatchesTotal
		s.CIDRIndex = 0
		s.CIDRTotal = len(batch.CIDRs)
		s.Message = fmt.Sprintf("扫描 %s 的 %d 个网段", batch.Label, len(batch.CIDRs))
	})

	events := core.AutoScanEvents{
		OnIPScanned: func(delta int, currentCIDR string, currentIP string, currentIdx int, totalCIDRs int) {
			r.mu.Lock()
			r.scannedIP += int64(delta)
			r.status.CurrentCIDR = currentCIDR
			r.status.CurrentIP = currentIP
			r.status.CIDRIndex = currentIdx
			r.status.CIDRTotal = totalCIDRs
			r.mu.Unlock()
		},
		OnCandidateDiscovered: func(string, string) {
			r.mu.Lock()
			r.status.Candidates++
			r.mu.Unlock()
		},
		OnTargetFound: func(res *types.DetectionResult, count int, ip string, handshakeMs int64, statusCode int) {
			r.addRow(rowFromResult(res, ip, batch, handshakeMs, statusCode))
		},
		OnLimitHit: func(maxTargets int) {
			r.setStatus(func(s *Status) {
				s.Message = fmt.Sprintf("已达目标数量上限 %d，正在收尾", maxTargets)
			})
		},
		OnLog: func(msg string) {
			r.setStatus(func(s *Status) { s.Message = msg })
		},
	}

	scanConfig := core.AutoScanConfig{
		CIDRs:      batch.CIDRs,
		MaxTargets: maxTargets,
		CheckAll:   false,
		Filter:     r.cfg.Filter,
		ASN:        batch.Label,
		Country:    batch.Country,
		Profile:    core.ProfileFast,
	}
	engine := core.NewAutoScanner(r.cfg.Engine, nil, r.cfg.Scanner, scanConfig, events)
	if _, err := engine.Run(ctx); err != nil && ctx.Err() == nil {
		r.setStatus(func(s *Status) { s.Message = "本批扫描出现异常：" + shortError(err) })
	}
}

// addRow 追加命中目标（按域名去重）
func (r *Runner) addRow(row Row) {
	key := strings.ToLower(strings.TrimSpace(row.Domain))
	if key == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen[key] {
		return
	}
	r.seen[key] = true
	r.rows = append(r.rows, row)
}

// rowFromResult 把检测结果转换为面板展示行
func rowFromResult(res *types.DetectionResult, ip string, batch *Batch, handshakeMs int64, statusCode int) Row {
	row := Row{
		IP:          ip,
		Stars:       core.CalculateTargetStars(res),
		HandshakeMS: handshakeMs,
		StatusCode:  statusCode,
		FoundAt:     time.Now(),
	}
	if res != nil {
		row.Domain = res.Domain
		if res.Certificate != nil {
			row.CertDays = res.Certificate.DaysUntilExpiry
		}
		if res.CDN != nil {
			row.IsCDN = res.CDN.IsCDN
			row.IsHot = res.CDN.IsHotWebsite
		}
		if res.Network != nil {
			row.StatusCode = res.Network.StatusCode
			row.Title = res.Network.PageTitle
			if handshakeMs == 0 {
				row.HandshakeMS = res.Network.HandshakeTime.Milliseconds()
			}
		}
		if res.TLS != nil && row.HandshakeMS == 0 {
			row.HandshakeMS = res.TLS.HandshakeTime.Milliseconds()
		}
		if res.Location != nil {
			row.Country = res.Location.Country
		}
	}
	if batch != nil {
		row.ASN = batch.Label
		if row.Country == "" {
			row.Country = batch.Country
		}
	}
	return row
}

// hitCount 返回当前命中数量
func (r *Runner) hitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rows)
}

// hitLimit 返回是否已达到目标数量上限
func (r *Runner) hitLimit() bool {
	return r.hitCount() >= r.cfg.Limit
}

// setStatus 在锁内更新状态
func (r *Runner) setStatus(mutate func(*Status)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	mutate(&r.status)
}

// updatePlan 更新计划统计
func (r *Runner) updatePlan(stats PlanStats) {
	r.setStatus(func(s *Status) {
		s.Plan = stats
		s.BatchTotal = stats.BatchesTotal
	})
}

// finish 结束扫描并写入终态
func (r *Runner) finish(phase Phase, message, reason string) {
	r.setStatus(func(s *Status) {
		s.Phase = phase
		s.Message = message
		s.Reason = reason
	})
}
