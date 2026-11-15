package panel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 面板内的卸载：按安装脚本创建的固定清单删除，避免误删用户数据
// （data/reality_targets.db 是资产数据库，systemd 守护进程另行停用）。

// releaseDataFiles install.sh 下载到 data/ 的离线规则库
var releaseDataFiles = []string{"Country.mmdb", "gfwlist.conf", "cdn_keywords.txt", "hot_websites.txt", "exclude_rules.txt"}

// UninstallKind 卸载目标的类型
type UninstallKind string

const (
	// UninstallFile 普通文件
	UninstallFile UninstallKind = "file"
	// UninstallDir 目录（连同内容一起删除）
	UninstallDir UninstallKind = "dir"
	// UninstallDirIfEmpty 目录（只在为空时删除，保留用户数据）
	UninstallDirIfEmpty UninstallKind = "dir-empty"
)

// UninstallTarget 一个卸载目标；Deep 表示仅在「彻底卸载」时删除
type UninstallTarget struct {
	Label string
	Path  string
	Kind  UninstallKind
	Deep  bool
}

// UninstallConfig 卸载涉及的路径；可注入，便于测试
type UninstallConfig struct {
	ExecPath     string
	RCMPath      string
	InstallDir   string
	SettingsPath string
}

// DefaultUninstallConfig 按当前运行环境推导卸载路径
func DefaultUninstallConfig(settingsPath string) UninstallConfig {
	cfg := UninstallConfig{SettingsPath: strings.TrimSpace(settingsPath)}
	if exe, err := os.Executable(); err == nil {
		cfg.ExecPath = exe
		cfg.InstallDir = filepath.Dir(exe)
	}
	cfg.RCMPath = DefaultRCMPath()
	return cfg
}

// DefaultRCMPath 返回快捷指令 rcm 的默认安装路径（与 install.sh 的 RC_BIN_DIR 一致）
func DefaultRCMPath() string {
	if binDir := strings.TrimSpace(os.Getenv("RC_BIN_DIR")); binDir != "" {
		return filepath.Join(binDir, "rcm")
	}
	return "/usr/local/bin/rcm"
}

// BuildUninstallTargets 生成卸载清单，只保留实际存在的路径
func BuildUninstallTargets(cfg UninstallConfig) []UninstallTarget {
	targets := make([]UninstallTarget, 0, 16)
	seen := make(map[string]struct{})
	add := func(target UninstallTarget) {
		if target.Path == "" {
			return
		}
		if _, exists := seen[target.Path]; exists {
			return
		}
		if _, err := os.Lstat(target.Path); err != nil {
			return
		}
		seen[target.Path] = struct{}{}
		targets = append(targets, target)
	}

	if execPath := strings.TrimSpace(cfg.ExecPath); execPath != "" {
		add(UninstallTarget{Label: "主程序", Path: execPath, Kind: UninstallFile})
		add(UninstallTarget{Label: "更新备份", Path: execPath + ".old", Kind: UninstallFile})
	}
	add(UninstallTarget{Label: "快捷指令 rcm", Path: cfg.RCMPath, Kind: UninstallFile})
	add(UninstallTarget{Label: "面板设置", Path: cfg.SettingsPath, Kind: UninstallFile})

	installDir := strings.TrimSpace(cfg.InstallDir)
	if installDir != "" {
		add(UninstallTarget{Label: "安装目录内 rcm", Path: filepath.Join(installDir, "rcm"), Kind: UninstallFile})
		add(UninstallTarget{Label: "配置文件", Path: filepath.Join(installDir, "config.yaml"), Kind: UninstallFile, Deep: true})
		for _, name := range releaseDataFiles {
			add(UninstallTarget{Label: "规则库 " + name, Path: filepath.Join(installDir, "data", name), Kind: UninstallFile, Deep: true})
		}
		add(UninstallTarget{Label: "data 目录（仅空）", Path: filepath.Join(installDir, "data"), Kind: UninstallDirIfEmpty, Deep: true})
		add(UninstallTarget{Label: "安装目录（仅空）", Path: installDir, Kind: UninstallDirIfEmpty, Deep: true})
	}
	return targets
}

// UninstallResult 卸载结果
type UninstallResult struct {
	Removed []string
	Failed  []string
}

// RemoveUninstallTargets 依次删除清单中的目标；目录非空或不存在不算失败
func RemoveUninstallTargets(targets []UninstallTarget) UninstallResult {
	result := UninstallResult{}
	for _, target := range targets {
		switch target.Kind {
		case UninstallDir:
			if err := os.RemoveAll(target.Path); err != nil {
				result.Failed = append(result.Failed, fmt.Sprintf("%s（%s）", target.Path, shortError(err)))
				continue
			}
		case UninstallDirIfEmpty:
			// 非空目录说明还留着用户数据，保留即可，不视为失败
			if err := os.Remove(target.Path); err != nil {
				continue
			}
		default:
			if err := os.Remove(target.Path); err != nil && !os.IsNotExist(err) {
				result.Failed = append(result.Failed, fmt.Sprintf("%s（%s）", target.Path, shortError(err)))
				continue
			}
		}
		result.Removed = append(result.Removed, target.Path)
	}
	return result
}

// splitUninstallTargets 把清单拆成「仅程序」与「彻底卸载额外」两组
func splitUninstallTargets(targets []UninstallTarget) (basic, deep []UninstallTarget) {
	for _, target := range targets {
		if target.Deep {
			deep = append(deep, target)
			continue
		}
		basic = append(basic, target)
	}
	return basic, deep
}

// isWithinDir 判断 path 是否就是 dir 或位于 dir 之下
func isWithinDir(path, dir string) bool {
	relative, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}
