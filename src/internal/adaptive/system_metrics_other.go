//go:build !linux

package adaptive

import "time"

type fallbackSystemMonitor struct{}

func newPlatformSystemMonitor() SystemMonitor {
	return &fallbackSystemMonitor{}
}

func (m *fallbackSystemMonitor) Sample() SystemMetrics {
	// 非 Linux 平台默认返回不可用占位（仅用于本地开发与测试）
	return SystemMetrics{
		Load1:      0,
		Load5:      0,
		CPUPercent: 0,
		SampleTime: time.Now(),
		Available:  false,
	}
}
