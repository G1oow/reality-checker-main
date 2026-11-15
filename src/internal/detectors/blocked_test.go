package detectors

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadGFWListFallbackToEmbeddedAsset(t *testing.T) {
	// 工作目录切到不存在 data/gfwlist.conf 的临时目录，强制走内置资源
	t.Chdir(t.TempDir())

	stage := NewBlockedStage()

	if len(stage.gfwlist) == 0 {
		t.Fatalf("期望从内置资源加载 GFW 名单，实际为 0 条")
	}
	if !stage.gfwlist["google.com"] {
		t.Errorf("内置 GFW 名单缺少 google.com 条目")
	}
	if blocked, _ := stage.checkBlocked("google.com"); !blocked {
		t.Errorf("期望 google.com 命中 GFW 名单")
	}
}

func TestLoadGFWListFromPlainDomainFile(t *testing.T) {
	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("创建测试 data 目录失败: %v", err)
	}
	content := "# 注释行\nexample.org\n\nexample.net\n"
	if err := os.WriteFile(filepath.Join(dataDir, "gfwlist.conf"), []byte(content), 0644); err != nil {
		t.Fatalf("写入测试 gfwlist.conf 失败: %v", err)
	}

	t.Chdir(tempDir)

	stage := NewBlockedStage()

	if !stage.gfwlist["example.org"] || !stage.gfwlist["example.net"] {
		t.Errorf("纯域名格式解析结果不符合预期: %v", stage.gfwlist)
	}
	if len(stage.gfwlist) != 2 {
		t.Errorf("期望解析出 2 条规则，实际 %d 条: %v", len(stage.gfwlist), stage.gfwlist)
	}
}
