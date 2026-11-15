package panel

import (
	"os"
	"path/filepath"
	"testing"

	"RealityChecker/internal/types"
)

func baseFilter() types.FilterConfig {
	return types.FilterConfig{
		RequireNoCN:          true,
		RequireNoCDN:         true,
		RequireNoHot:         true,
		RequireNoDefaultPage: true,
		MinStars:             1,
		MaxHandshakeMS:       500,
		MinCertDays:          10,
		ScanThreads:          200,
		ScanTimeout:          3,
		ExcludeSuffixes:      []string{".local"},
		ExcludeStatus:        []int{404},
	}
}

func TestSettingsNormalizeClampsValues(t *testing.T) {
	settings := Settings{
		Mode:       Mode("bogus"),
		Family:     FamilyChoice("bogus"),
		Country:    "deutschland",
		Limit:      999,
		Threads:    99999,
		TimeoutSec: 99,
		Filter:     FilterOptions{MinStars: 42, MaxHandshakeMS: -1, MinCertDays: -5},
	}
	settings.Normalize()

	if settings.Mode != ModeEntry {
		t.Fatalf("非法模式应回落到入口模式，实际 %q", settings.Mode)
	}
	if settings.Family != FamilyBoth {
		t.Fatalf("非法协议族应回落到 both，实际 %q", settings.Family)
	}
	if settings.Country != "" {
		t.Fatalf("非法国家代码应被清空，实际 %q", settings.Country)
	}
	if settings.Limit != MaxLimit {
		t.Fatalf("limit 应被夹到 %d，实际 %d", MaxLimit, settings.Limit)
	}
	if settings.Threads != MaxThreads {
		t.Fatalf("并发应被夹到 %d，实际 %d", MaxThreads, settings.Threads)
	}
	if settings.TimeoutSec != MaxTimeout {
		t.Fatalf("超时应被夹到 %d，实际 %d", MaxTimeout, settings.TimeoutSec)
	}
	if settings.Filter.MinStars != 5 {
		t.Fatalf("星级应被夹到 5，实际 %d", settings.Filter.MinStars)
	}
	if settings.Filter.MaxHandshakeMS != 0 || settings.Filter.MinCertDays != 0 {
		t.Fatalf("负值应归零，实际 %d / %d", settings.Filter.MaxHandshakeMS, settings.Filter.MinCertDays)
	}
}

func TestSettingsLimitUpperBound(t *testing.T) {
	settings := DefaultSettings(baseFilter())
	settings.Limit = MaxLimit + 1
	settings.Normalize()
	if settings.Limit != MaxLimit {
		t.Fatalf("limit 上限应为 %d，实际 %d", MaxLimit, settings.Limit)
	}
	settings.Limit = 0
	settings.Normalize()
	if settings.Limit != 5 {
		t.Fatalf("limit 为 0 时应回落默认 5，实际 %d", settings.Limit)
	}
}

func TestSettingsSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "panel.json")

	settings := DefaultSettings(baseFilter())
	settings.Mode = ModeCountry
	settings.Country = "de"
	settings.Family = FamilyV6
	settings.Limit = 8
	settings.Threads = 500
	settings.TimeoutSec = 5
	settings.Filter.AllowCDN = true
	settings.Filter.MinStars = 4
	if err := settings.Save(path); err != nil {
		t.Fatalf("保存设置失败: %v", err)
	}

	loaded, err := LoadSettings(path, baseFilter())
	if err != nil {
		t.Fatalf("读取设置失败: %v", err)
	}
	if loaded.Mode != ModeCountry || loaded.Country != "DE" || loaded.Family != FamilyV6 {
		t.Fatalf("模式/国家/协议族未正确往返: %+v", loaded)
	}
	if loaded.Limit != 8 || loaded.Threads != 500 || loaded.TimeoutSec != 5 {
		t.Fatalf("数值参数未正确往返: %+v", loaded)
	}
	if !loaded.Filter.AllowCDN || loaded.Filter.MinStars != 4 {
		t.Fatalf("过滤开关未正确往返: %+v", loaded.Filter)
	}
}

func TestLoadSettingsMissingFileReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	settings, err := LoadSettings(path, baseFilter())
	if err != nil {
		t.Fatalf("文件不存在时不应报错: %v", err)
	}
	if settings.Mode != ModeEntry || settings.Limit != 5 {
		t.Fatalf("应返回默认值，实际 %+v", settings)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("读取缺失设置不应创建文件")
	}
}

func TestLoadSettingsInvalidJSONFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadSettings(path, baseFilter())
	if err == nil {
		t.Fatalf("非法 JSON 应返回错误")
	}
	if settings.Limit != 5 {
		t.Fatalf("非法 JSON 应回落到默认值，实际 %+v", settings)
	}
}

func TestSettingsFilterConfigOverlay(t *testing.T) {
	base := baseFilter()
	settings := DefaultSettings(base)
	settings.Family = FamilyV4
	settings.Threads = 300
	settings.TimeoutSec = 7
	settings.Filter.AllowCDN = true
	settings.Filter.AllowHot = false
	settings.Filter.ExcludeCN = false
	settings.Filter.MinStars = 4
	settings.Filter.MaxHandshakeMS = 250
	settings.Filter.MinCertDays = 30

	got := settings.FilterConfig(base)
	if got.RequireNoCDN {
		t.Fatalf("允许 CDN 时 RequireNoCDN 应为 false")
	}
	if !got.RequireNoHot {
		t.Fatalf("禁止热门大站时 RequireNoHot 应为 true")
	}
	if got.RequireNoCN {
		t.Fatalf("不排除国内站点时 RequireNoCN 应为 false")
	}
	if !got.IPv4Only || got.IPv6Only {
		t.Fatalf("仅 IPv4 选择应映射为 IPv4Only，实际 %v/%v", got.IPv4Only, got.IPv6Only)
	}
	if got.ScanThreads != 300 || got.ScanTimeout != 7 {
		t.Fatalf("并发/超时未透传：%d/%d", got.ScanThreads, got.ScanTimeout)
	}
	if got.MinStars != 4 || got.MaxHandshakeMS != 250 || got.MinCertDays != 30 {
		t.Fatalf("过滤阈值未透传：%+v", got)
	}
	if got.UseCache || got.VerifyCache || !got.NoResume {
		t.Fatalf("面板必须禁用资产库缓存与断点：%+v", got)
	}
	// 配置文件中未被面板接管的排除策略必须保留
	if len(got.ExcludeSuffixes) != 1 || len(got.ExcludeStatus) != 1 {
		t.Fatalf("基础排除策略应保留：%+v", got)
	}
}

func TestSettingsFamilies(t *testing.T) {
	cases := []struct {
		choice FamilyChoice
		want   int
	}{
		{FamilyV4, 1},
		{FamilyV6, 1},
		{FamilyBoth, 2},
	}
	for _, item := range cases {
		settings := DefaultSettings(baseFilter())
		settings.Family = item.choice
		families := settings.Families()
		if len(families) != item.want {
			t.Fatalf("%s 应展开 %d 个协议族，实际 %d", item.choice, item.want, len(families))
		}
	}
}

func TestSettingsValidate(t *testing.T) {
	settings := DefaultSettings(baseFilter())
	if reason := settings.Validate(); reason != "" {
		t.Fatalf("默认入口模式应可直接扫描，实际 %q", reason)
	}
	settings.Mode = ModeCountry
	if reason := settings.Validate(); reason == "" {
		t.Fatalf("国家模式缺少国家代码时必须阻塞")
	}
	settings.Country = "JP"
	settings.Normalize()
	if reason := settings.Validate(); reason != "" {
		t.Fatalf("补齐国家代码后应可扫描，实际 %q", reason)
	}
}
