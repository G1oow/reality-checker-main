package panel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newUninstallFixture 构造一个安装目录：主程序、rcm、配置、规则库与用户资产数据库
func newUninstallFixture(t *testing.T) UninstallConfig {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "reality-checker-main"), "binary")
	writeTestFile(t, filepath.Join(dir, "rcm"), "#!/usr/bin/env bash")
	writeTestFile(t, filepath.Join(dir, "config.yaml"), "filter: {}")
	writeTestFile(t, filepath.Join(dir, "data", "Country.mmdb"), "mmdb")
	writeTestFile(t, filepath.Join(dir, "data", "exclude_rules.txt"), "rules")
	writeTestFile(t, filepath.Join(dir, "data", "reality_targets.db"), "sqlite")
	settingsPath := filepath.Join(dir, "panel.json")
	writeTestFile(t, settingsPath, "{}")
	return UninstallConfig{
		ExecPath:     filepath.Join(dir, "reality-checker-main"),
		RCMPath:      filepath.Join(dir, "rcm"),
		InstallDir:   dir,
		SettingsPath: settingsPath,
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入文件失败: %v", err)
	}
}

func TestBuildUninstallTargetsKeepsUserDatabase(t *testing.T) {
	cfg := newUninstallFixture(t)
	targets := BuildUninstallTargets(cfg)
	if len(targets) == 0 {
		t.Fatalf("应生成卸载清单")
	}

	paths := make(map[string]UninstallTarget, len(targets))
	for _, target := range targets {
		paths[target.Path] = target
	}
	for _, want := range []string{cfg.ExecPath, cfg.RCMPath, cfg.SettingsPath, filepath.Join(cfg.InstallDir, "config.yaml")} {
		if _, ok := paths[want]; !ok {
			t.Fatalf("卸载清单缺少 %s：%+v", want, targets)
		}
	}
	if _, ok := paths[filepath.Join(cfg.InstallDir, "data", "reality_targets.db")]; ok {
		t.Fatalf("卸载清单不应包含用户资产数据库")
	}

	basic, deep := splitUninstallTargets(targets)
	for _, target := range basic {
		if target.Deep {
			t.Fatalf("「仅卸载程序」不应包含彻底卸载项：%+v", target)
		}
	}
	if len(deep) == 0 {
		t.Fatalf("应存在彻底卸载项")
	}
	for _, target := range basic {
		if strings.HasSuffix(target.Path, "config.yaml") {
			t.Fatalf("配置文件应属于彻底卸载：%+v", target)
		}
	}
}

func TestRemoveUninstallTargetsKeepsConfigAndData(t *testing.T) {
	cfg := newUninstallFixture(t)
	basic, _ := splitUninstallTargets(BuildUninstallTargets(cfg))
	result := RemoveUninstallTargets(basic)
	if len(result.Failed) != 0 {
		t.Fatalf("卸载不应失败：%+v", result.Failed)
	}
	for _, path := range []string{cfg.ExecPath, cfg.RCMPath, cfg.SettingsPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s 应被删除", path)
		}
	}
	for _, kept := range []string{
		filepath.Join(cfg.InstallDir, "config.yaml"),
		filepath.Join(cfg.InstallDir, "data", "Country.mmdb"),
		filepath.Join(cfg.InstallDir, "data", "reality_targets.db"),
	} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s 应保留：%v", kept, err)
		}
	}
}

func TestRemoveUninstallTargetsDeepKeepsAssetDatabase(t *testing.T) {
	cfg := newUninstallFixture(t)
	result := RemoveUninstallTargets(BuildUninstallTargets(cfg))
	if len(result.Failed) != 0 {
		t.Fatalf("卸载不应失败：%+v", result.Failed)
	}
	for _, removed := range []string{
		cfg.ExecPath,
		cfg.RCMPath,
		cfg.SettingsPath,
		filepath.Join(cfg.InstallDir, "config.yaml"),
		filepath.Join(cfg.InstallDir, "data", "Country.mmdb"),
		filepath.Join(cfg.InstallDir, "data", "exclude_rules.txt"),
	} {
		if _, err := os.Stat(removed); !os.IsNotExist(err) {
			t.Fatalf("%s 应被删除", removed)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.InstallDir, "data", "reality_targets.db")); err != nil {
		t.Fatalf("用户资产数据库应保留：%v", err)
	}
	if _, err := os.Stat(cfg.InstallDir); err != nil {
		t.Fatalf("还有用户数据时不应删除安装目录：%v", err)
	}
}

func TestBuildUninstallTargetsSkipsMissingPaths(t *testing.T) {
	dir := t.TempDir()
	targets := BuildUninstallTargets(UninstallConfig{
		ExecPath:     filepath.Join(dir, "missing-binary"),
		RCMPath:      filepath.Join(dir, "missing-rcm"),
		SettingsPath: filepath.Join(dir, "missing-settings.json"),
	})
	if len(targets) != 0 {
		t.Fatalf("不存在的路径不应进入卸载清单：%+v", targets)
	}
}

func TestIsWithinDir(t *testing.T) {
	cases := []struct {
		path     string
		dir      string
		expected bool
	}{
		{filepath.FromSlash("/opt/rc/data"), filepath.FromSlash("/opt/rc"), true},
		{filepath.FromSlash("/opt/rc"), filepath.FromSlash("/opt/rc"), true},
		{filepath.FromSlash("/opt"), filepath.FromSlash("/opt/rc"), false},
		{filepath.FromSlash("/opt/rc2"), filepath.FromSlash("/opt/rc"), false},
	}
	for _, testCase := range cases {
		if got := isWithinDir(testCase.path, testCase.dir); got != testCase.expected {
			t.Fatalf("isWithinDir(%q, %q) = %v，期望 %v", testCase.path, testCase.dir, got, testCase.expected)
		}
	}
}
