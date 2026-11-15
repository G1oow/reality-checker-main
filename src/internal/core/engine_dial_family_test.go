package core

import (
	"testing"

	"RealityChecker/internal/types"
)

func TestEngineSetDialFamily(t *testing.T) {
	engine := NewEngine(&types.Config{})
	if err := engine.Start(); err != nil {
		t.Fatalf("启动引擎失败: %v", err)
	}
	defer engine.Stop()

	if got := engine.DialFamily(); got != "" {
		t.Fatalf("默认应不限定协议族，实际 %q", got)
	}

	engine.SetDialFamily("v6")
	if got := engine.DialFamily(); got != "ipv6" {
		t.Fatalf("别名 v6 应归一化为 ipv6，实际 %q", got)
	}
	engine.SetDialFamily("ipv6")
	if got := engine.DialFamily(); got != "ipv6" {
		t.Fatalf("重复设置应保持 ipv6，实际 %q", got)
	}
	if cfg := engine.config; cfg == nil || cfg.Network.Family != "ipv6" {
		t.Fatalf("协议族应写入引擎配置，实际 %+v", cfg)
	}
	engine.SetDialFamily("")
	if got := engine.DialFamily(); got != "" {
		t.Fatalf("空值应清除固定的协议族，实际 %q", got)
	}
}

// 零值引擎（测试桩）没有配置与连接层，切换协议族必须是安全的空操作。
func TestEngineSetDialFamilyOnZeroValue(t *testing.T) {
	engine := &Engine{}
	engine.SetDialFamily("ipv6")
	if got := engine.DialFamily(); got != "" {
		t.Fatalf("零值引擎应保持不限定协议族，实际 %q", got)
	}
}
