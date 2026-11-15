package core

import (
	"context"
	"fmt"
	"sync"

	"RealityChecker/internal/logger"
	"RealityChecker/internal/network"
	"RealityChecker/internal/types"
)

// Engine 主检测引擎（简化版本）
type Engine struct {
	config      *types.Config
	pipeline    *Pipeline
	connections *network.ConnectionManager
	mu          sync.RWMutex
	running     bool
}

// NewEngine 创建新的检测引擎 (默认标准画像)
func NewEngine(config *types.Config) *Engine {
	return NewEngineWithProfile(config, ProfileStandard)
}

// NewEngineWithProfile 指定编排画像创建检测引擎
func NewEngineWithProfile(config *types.Config, profile PipelineProfile) *Engine {
	engine := &Engine{
		config: config,
	}

	engine.connections = network.NewConnectionManager(config)
	engine.pipeline = NewPipelineWithProfile(engine.connections, config, profile)

	return engine
}

// Start 启动引擎（简化版本）
func (e *Engine) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.running {
		return fmt.Errorf("引擎已在运行")
	}

	// 启动连接管理器
	if err := e.connections.Start(); err != nil {
		return fmt.Errorf("启动连接管理器失败: %v", err)
	}

	// 缓存管理器已移除

	e.running = true
	return nil
}

// Stop 停止引擎（简化版本）
func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.running {
		return nil
	}

	// 缓存管理器已移除

	// 停止连接管理器
	if err := e.connections.Stop(); err != nil {
		logger.Error("连接管理器停止失败：%v", err)
	}

	e.running = false
	return nil
}

// CheckDomain 检测单个域名（直接使用pipeline，简化架构）
func (e *Engine) CheckDomain(ctx context.Context, domain string) (*types.DetectionResult, error) {
	if !e.running {
		return nil, fmt.Errorf("引擎未运行")
	}

	return e.pipeline.Execute(ctx, domain)
}

// CheckDomains 批量检测域名（移除并发控制，由调用方管理）
func (e *Engine) CheckDomains(ctx context.Context, domains []string) ([]*types.DetectionResult, error) {
	if !e.running {
		return nil, fmt.Errorf("引擎未运行")
	}

	if len(domains) == 0 {
		return []*types.DetectionResult{}, nil
	}

	// 移除Engine层的并发控制，由Batch Manager统一管理
	results := make([]*types.DetectionResult, len(domains))

	for i, domain := range domains {
		// 直接执行检测，不进行并发控制
		result, err := e.pipeline.Execute(ctx, domain)
		if err != nil {
			result = &types.DetectionResult{
				Domain: domain,
				Error:  err,
			}
		}
		results[i] = result
	}

	return results, nil
}

// CheckDomainsStream 流式批量检测域名
func (e *Engine) CheckDomainsStream(ctx context.Context, domains []string) (<-chan *types.DetectionResult, error) {
	if !e.running {
		return nil, fmt.Errorf("引擎未运行")
	}

	resultChan := make(chan *types.DetectionResult, len(domains))

	go func() {
		defer close(resultChan)

		semaphore := make(chan struct{}, int(e.config.Concurrency.MaxConcurrent))
		var wg sync.WaitGroup

		for _, domain := range domains {
			wg.Add(1)
			go func(domain string) {
				defer wg.Done()

				semaphore <- struct{}{}
				defer func() { <-semaphore }()

				result, err := e.pipeline.Execute(ctx, domain)
				if err != nil {
					result = &types.DetectionResult{
						Domain: domain,
						Error:  err,
					}
				}

				select {
				case resultChan <- result:
				case <-ctx.Done():
					return
				}
			}(domain)
		}

		wg.Wait()
	}()

	return resultChan, nil
}

// GetPipeline 获取底层的检测流水线实例
func (e *Engine) GetPipeline() *Pipeline {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.pipeline
}

// Profile 获取当前流水线画像
func (e *Engine) Profile() PipelineProfile {
	if e.pipeline == nil {
		return ProfileStandard
	}
	return e.pipeline.Profile()
}

// SetProfile 动态切换底层流水线的编排画像
func (e *Engine) SetProfile(profile PipelineProfile) {
	if e.pipeline != nil {
		e.pipeline.SetProfile(profile)
	}
}

// SetDialFamily 设置目标连接协议族（""/"ipv4"/"ipv6"），并按需重建连接管理器。
// 必须在两次扫描之间调用（扫描进行中切换会造成数据竞争）。
func (e *Engine) SetDialFamily(family string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	normalized := network.NormalizeDialFamily(family)
	if e.config == nil {
		// 零值引擎（测试桩）没有连接层可切换
		return
	}
	if network.NormalizeDialFamily(e.config.Network.Family) == normalized && e.connections != nil {
		return
	}
	e.config.Network.Family = normalized

	if e.connections != nil {
		_ = e.connections.Stop()
	}
	e.connections = network.NewConnectionManager(e.config)
	_ = e.connections.Start()
	if e.pipeline != nil {
		e.pipeline.SetConnections(e.connections)
	}
}

// DialFamily 返回当前目标连接协议族配置
func (e *Engine) DialFamily() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.config == nil {
		return ""
	}
	return network.NormalizeDialFamily(e.config.Network.Family)
}

// GetStats 获取引擎统计信息
func (e *Engine) GetStats() *EngineStats {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return &EngineStats{
		Running:     e.running,
		Connections: e.connections.GetStats(),
	}
}

// EngineStats 引擎统计信息
type EngineStats struct {
	Running     bool                   `json:"running"`
	Connections *types.ConnectionStats `json:"connections"`
}
