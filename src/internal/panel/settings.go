package panel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"RealityChecker/internal/asn"
	"RealityChecker/internal/network"
	"RealityChecker/internal/types"
)

// Mode 面板扫描模式
type Mode string

const (
	// ModeEntry 按 VPS 入口 IP 的 ASN 扫描
	ModeEntry Mode = "entry"
	// ModeCountry 按国家/地区的 ASN 清单扫描
	ModeCountry Mode = "country"
)

// FamilyChoice 入口协议族选择
type FamilyChoice string

const (
	FamilyV4   FamilyChoice = "v4"
	FamilyV6   FamilyChoice = "v6"
	FamilyBoth FamilyChoice = "both"
)

// 目标数量与参数边界
const (
	MinLimit   = 1
	MaxLimit   = 100
	MinThreads = 10
	MaxThreads = 2000
	MinTimeout = 1
	MaxTimeout = 15
)

// LimitPresets 目标数量预设档位
var LimitPresets = []int{1, 3, 5, 10, 20, 50}

// LimitChoices 目标数量可选档位（预设 + 自定义步进）
var LimitChoices = []int{1, 2, 3, 5, 8, 10, 20, 30, 50, 100}

// ThreadChoices 并发档位
var ThreadChoices = []int{50, 100, 200, 300, 500, 800, 1000, 1500, 2000}

// TimeoutChoices 单 IP 超时档位（秒）
var TimeoutChoices = []int{1, 2, 3, 5, 8, 10, 15}

// FilterOptions 面板暴露的进阶过滤开关
type FilterOptions struct {
	MinStars           int   `json:"min_stars"`            // 最低推荐星级 (0 = 不限)
	MaxHandshakeMS     int64 `json:"max_handshake_ms"`     // 握手延迟上限毫秒 (0 = 不限)
	MinCertDays        int   `json:"min_cert_days"`        // 证书剩余天数下限 (0 = 不限)
	AllowCDN           bool  `json:"allow_cdn"`            // 是否允许 CDN 站点
	AllowHot           bool  `json:"allow_hot"`            // 是否允许超级热门大站
	ExcludeCN          bool  `json:"exclude_cn"`           // 是否排除国内站点
	ExcludeDefaultPage bool  `json:"exclude_default_page"` // 是否排除 Web 默认页
}

// Settings 面板可调参数（持久化到本地设置文件）
type Settings struct {
	Mode       Mode          `json:"mode"`
	Country    string        `json:"country"`
	Family     FamilyChoice  `json:"family"`
	Limit      int           `json:"limit"`
	Threads    int           `json:"threads"`
	TimeoutSec int           `json:"timeout_sec"`
	Filter     FilterOptions `json:"filter"`
}

// DefaultSettings 依据全局配置生成面板默认参数
func DefaultSettings(base types.FilterConfig) Settings {
	minStars := 3
	if base.MinStars > 0 {
		minStars = base.MinStars
	}
	return Settings{
		Mode:       ModeEntry,
		Country:    "",
		Family:     FamilyBoth,
		Limit:      5,
		Threads:    200,
		TimeoutSec: 3,
		Filter: FilterOptions{
			MinStars:           minStars,
			MaxHandshakeMS:     base.MaxHandshakeMS,
			MinCertDays:        base.MinCertDays,
			AllowCDN:           !base.RequireNoCDN,
			AllowHot:           !base.RequireNoHot,
			ExcludeCN:          base.RequireNoCN,
			ExcludeDefaultPage: base.RequireNoDefaultPage,
		},
	}
}

// Normalize 修正越界或非法的面板参数
func (s *Settings) Normalize() {
	if s.Mode != ModeCountry {
		s.Mode = ModeEntry
	}
	if s.Family != FamilyV4 && s.Family != FamilyV6 && s.Family != FamilyBoth {
		s.Family = FamilyBoth
	}
	s.Country = strings.ToUpper(strings.TrimSpace(s.Country))
	if s.Country != "" && !asn.IsCountryCode(s.Country) {
		s.Country = ""
	}
	if s.Limit <= 0 {
		s.Limit = 5
	}
	s.Limit = clampInt(s.Limit, MinLimit, MaxLimit)
	if s.Threads <= 0 {
		s.Threads = 200
	}
	s.Threads = clampInt(s.Threads, MinThreads, MaxThreads)
	if s.TimeoutSec <= 0 {
		s.TimeoutSec = 3
	}
	s.TimeoutSec = clampInt(s.TimeoutSec, MinTimeout, MaxTimeout)
	if s.Filter.MinStars < 0 || s.Filter.MinStars > 5 {
		s.Filter.MinStars = clampInt(s.Filter.MinStars, 0, 5)
	}
	if s.Filter.MaxHandshakeMS < 0 {
		s.Filter.MaxHandshakeMS = 0
	}
	if s.Filter.MaxHandshakeMS > 5000 {
		s.Filter.MaxHandshakeMS = 5000
	}
	if s.Filter.MinCertDays < 0 {
		s.Filter.MinCertDays = 0
	}
	if s.Filter.MinCertDays > 3650 {
		s.Filter.MinCertDays = 3650
	}
}

// Families 展开当前协议族选择
func (s Settings) Families() []network.Family {
	switch s.Family {
	case FamilyV4:
		return []network.Family{network.FamilyIPv4}
	case FamilyV6:
		return []network.Family{network.FamilyIPv6}
	default:
		return []network.Family{network.FamilyIPv4, network.FamilyIPv6}
	}
}

// FilterConfig 把面板参数叠加到全局过滤配置上（保留配置里的排除规则与后缀策略）
func (s Settings) FilterConfig(base types.FilterConfig) types.FilterConfig {
	fc := base
	fc.RequireNoCN = s.Filter.ExcludeCN
	fc.RequireNoCDN = !s.Filter.AllowCDN
	fc.RequireNoHot = !s.Filter.AllowHot
	fc.RequireNoDefaultPage = s.Filter.ExcludeDefaultPage
	fc.MinStars = s.Filter.MinStars
	fc.MaxHandshakeMS = s.Filter.MaxHandshakeMS
	fc.MinCertDays = s.Filter.MinCertDays
	fc.ScanThreads = s.Threads
	fc.ScanTimeout = s.TimeoutSec

	// 面板只在内存中扫描，不读写资产库与断点
	fc.UseCache = false
	fc.VerifyCache = false
	fc.NoResume = true
	fc.Verbose = false

	switch s.Family {
	case FamilyV4:
		fc.IPv4Only, fc.IPv6Only = true, false
	case FamilyV6:
		fc.IPv4Only, fc.IPv6Only = false, true
	default:
		fc.IPv4Only, fc.IPv6Only = false, false
	}
	return fc
}

// Validate 返回阻塞启动扫描的原因（空表示可以开始）
func (s Settings) Validate() string {
	if s.Mode == ModeCountry {
		if s.Country == "" {
			return "请先填写国家/地区代码（两位 ISO 代码，如 DE、JP）"
		}
	}
	if s.Limit < MinLimit || s.Limit > MaxLimit {
		return fmt.Sprintf("目标数量必须在 %d~%d 之间", MinLimit, MaxLimit)
	}
	return ""
}

// DefaultSettingsPath 返回面板设置文件路径
func DefaultSettingsPath() string {
	if custom := strings.TrimSpace(os.Getenv("RCM_SETTINGS")); custom != "" {
		return custom
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "reality-checker", "panel.json")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "reality-checker", "panel.json")
	}
	return "panel.json"
}

// LoadSettings 读取面板设置；文件不存在时返回默认值
func LoadSettings(path string, base types.FilterConfig) (Settings, error) {
	settings := DefaultSettings(base)
	if strings.TrimSpace(path) == "" {
		path = DefaultSettingsPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return settings, nil
		}
		return settings, err
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return DefaultSettings(base), fmt.Errorf("解析面板设置失败: %w", err)
	}
	settings.Normalize()
	return settings, nil
}

// Save 持久化面板设置到本地文件
func (s Settings) Save(path string) error {
	if strings.TrimSpace(path) == "" {
		path = DefaultSettingsPath()
	}
	s.Normalize()
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建设置目录失败: %w", err)
		}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
