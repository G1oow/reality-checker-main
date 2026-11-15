package panel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"RealityChecker/internal/asn"
	"RealityChecker/internal/core"
	"RealityChecker/internal/network"
	"RealityChecker/internal/scanner"
	"RealityChecker/internal/types"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oschwald/geoip2-golang"
)

func newTestModel(t *testing.T, entries []EntryOption) (*Model, string) {
	t.Helper()
	settingsPath := filepath.Join(t.TempDir(), "panel.json")
	deps := Deps{
		BaseCfg: types.Config{Filter: baseFilter()},
		Probe: func(context.Context, *geoip2.Reader, asn.ASNResolver) []EntryOption {
			return entries
		},
	}
	model := NewModel(context.Background(), deps, DefaultSettings(baseFilter()), settingsPath)
	model.width, model.height = 120, 44
	model.SetEntries(entries)
	return model, settingsPath
}

// press 依次发送按键，模拟真实键盘交互
func press(t *testing.T, model *Model, keys ...string) *Model {
	t.Helper()
	for _, key := range keys {
		var msg tea.KeyMsg
		switch key {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "left":
			msg = tea.KeyMsg{Type: tea.KeyLeft}
		case "right":
			msg = tea.KeyMsg{Type: tea.KeyRight}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "space":
			msg = tea.KeyMsg{Type: tea.KeySpace}
		case "backspace":
			msg = tea.KeyMsg{Type: tea.KeyBackspace}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
		}
		updated, _ := model.Update(msg)
		next, ok := updated.(*Model)
		if !ok {
			t.Fatalf("Update 返回了非面板模型")
		}
		model = next
	}
	return model
}

func viewText(model *Model) string {
	return StripANSI(model.View())
}

func sampleEntries() []EntryOption {
	return []EntryOption{
		entryOption(network.FamilyIPv4, "198.51.100.7", 64500, []string{"198.51.100.0/24"}, true, ""),
		entryOption(network.FamilyIPv6, "", 0, nil, false, "无 IPv6 出口"),
	}
}

func TestViewModelRendersCoreSections(t *testing.T) {
	model, _ := newTestModel(t, sampleEntries())
	text := viewText(model)

	for _, want := range []string{
		"R-C-M 面板",
		"扫描参数",
		"扫描模式",
		"校验协议族",
		"目标数量 limit",
		"扫描并发",
		"单 IP 超时",
		"最低星级",
		"握手延迟上限",
		"证书剩余天数",
		"允许 CDN",
		"允许热门大站",
		"排除国内站点",
		"入口与进度",
		"已扫描",
		"命中",
		"当前网段",
		"耗时",
		"命中目标",
		"域名",
		"s 开始扫描",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("面板视图缺少 %q\n%s", want, text)
		}
	}
	if !strings.Contains(text, "IPv4") || !strings.Contains(text, "IPv6 不可用") {
		t.Fatalf("入口区应分别展示可用与不可用的协议族\n%s", text)
	}
}

func TestModelModeSwitchAndCountryInput(t *testing.T) {
	model, settingsPath := newTestModel(t, sampleEntries())

	model.cursor = rowMode
	model = press(t, model, "right")
	if model.Settings().Mode != ModeCountry {
		t.Fatalf("右方向键应切到国家模式，实际 %q", model.Settings().Mode)
	}
	if !strings.Contains(viewText(model), "按国家/地区 ASN") {
		t.Fatalf("国家模式标题未生效")
	}
	if !strings.Contains(viewText(model), "未设置（回车输入）") {
		t.Fatalf("国家代码未填写时应提示输入")
	}

	model.cursor = rowCountry
	model = press(t, model, "enter", "d", "e", "enter")
	if model.Settings().Country != "DE" {
		t.Fatalf("国家代码输入应被规范化保存，实际 %q", model.Settings().Country)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("设置应被持久化: %v", err)
	}
	if !strings.Contains(string(data), "\"country\": \"DE\"") {
		t.Fatalf("持久化内容应包含国家代码：%s", string(data))
	}
	model.cursor = rowMode
	model = press(t, model, "left")
	if model.Settings().Mode != ModeEntry {
		t.Fatalf("左方向键应切回入口模式")
	}
}

func TestModelLimitSelectionAndUpperBound(t *testing.T) {
	model, settingsPath := newTestModel(t, sampleEntries())
	model.cursor = rowLimit
	model.settings.Limit = 3

	model = press(t, model, "right")
	if model.Settings().Limit != 5 {
		t.Fatalf("limit 应从 3 调到 5，实际 %d", model.Settings().Limit)
	}
	model.settings.Limit = 50
	model = press(t, model, "right")
	if model.Settings().Limit != MaxLimit {
		t.Fatalf("limit 应可调到上限 %d，实际 %d", MaxLimit, model.Settings().Limit)
	}
	model = press(t, model, "right")
	if model.Settings().Limit != MaxLimit {
		t.Fatalf("到达上限后不应回绕，实际 %d", model.Settings().Limit)
	}
	model = press(t, model, "left")
	if model.Settings().Limit != 50 {
		t.Fatalf("向左应回到上一档 50，实际 %d", model.Settings().Limit)
	}
	if !strings.Contains(viewText(model), "目标数量 limit") {
		t.Fatalf("视图应展示 limit 行")
	}
	data, _ := os.ReadFile(settingsPath)
	if !strings.Contains(string(data), "\"limit\": 50") {
		t.Fatalf("limit 应持久化：%s", string(data))
	}
}

func TestModelFamilySelectionSteps(t *testing.T) {
	model, _ := newTestModel(t, sampleEntries())
	model.cursor = rowFamily
	model.settings.Family = FamilyBoth

	model = press(t, model, "left")
	if model.Settings().Family != FamilyV6 {
		t.Fatalf("应前进到 IPv6，实际 %q", model.Settings().Family)
	}
	model = press(t, model, "left")
	if model.Settings().Family != FamilyV4 {
		t.Fatalf("应前进到 IPv4，实际 %q", model.Settings().Family)
	}
	model = press(t, model, "left")
	if model.Settings().Family != FamilyV4 {
		t.Fatalf("到达边界后不应回绕，实际 %q", model.Settings().Family)
	}
	if !strings.Contains(viewText(model), "仅 IPv4") {
		t.Fatalf("视图应展示仅 IPv4")
	}
}

func TestModelFilterToggles(t *testing.T) {
	model, settingsPath := newTestModel(t, sampleEntries())

	model.cursor = rowAllowCDN
	model = press(t, model, "space")
	if !model.Settings().Filter.AllowCDN {
		t.Fatalf("空格应切换允许 CDN")
	}
	model.cursor = rowExcludeCN
	model = press(t, model, "space")
	if model.Settings().Filter.ExcludeCN {
		t.Fatalf("空格应切换排除国内站点")
	}
	model.cursor = rowMinStars
	model.settings.Filter.MinStars = 0
	model = press(t, model, "right", "right")
	if model.Settings().Filter.MinStars != 2 {
		t.Fatalf("最低星级应可递增，实际 %d", model.Settings().Filter.MinStars)
	}
	model.cursor = rowTimeout
	model.settings.TimeoutSec = 15
	model = press(t, model, "right")
	if model.Settings().TimeoutSec != MaxTimeout {
		t.Fatalf("超时应停在 %d，实际 %d", MaxTimeout, model.Settings().TimeoutSec)
	}

	text := viewText(model)
	if !strings.Contains(text, "允许 CDN") {
		t.Fatalf("过滤项应展示在参数区\n%s", text)
	}
	data, _ := os.ReadFile(settingsPath)
	if !strings.Contains(string(data), "\"allow_cdn\": true") {
		t.Fatalf("过滤开关应持久化：%s", string(data))
	}
}

func TestModelStartScanBlockedWithoutEngine(t *testing.T) {
	model, _ := newTestModel(t, sampleEntries())
	model = press(t, model, "s")

	if !strings.Contains(viewText(model), "扫描引擎未初始化") {
		t.Fatalf("缺少扫描引擎时应给出提示\n%s", viewText(model))
	}
	if model.Status().Phase != PhaseIdle {
		t.Fatalf("未启动扫描时阶段应为 idle，实际 %s", model.Status().Phase)
	}
}

func TestModelStartScanCountryRequiresCountryCode(t *testing.T) {
	model, _ := newTestModel(t, sampleEntries())
	model.settings.Mode = ModeCountry
	model.settings.Country = ""
	model.cursor = rowMode

	model = press(t, model, "s")
	if !strings.Contains(viewText(model), "请先填写国家/地区代码") {
		t.Fatalf("国家模式缺少国家代码时应阻塞\n%s", viewText(model))
	}
}

func TestModelStartScanFailsFastWhenCountryLookupFails(t *testing.T) {
	deps := Deps{
		BaseCfg: types.Config{Filter: baseFilter()},
		Engine:  &core.Engine{},
		Scanner: &scanner.Scanner{},
		Country: &fakeCountryClient{countryErr: errors.New("RIPE Stat 返回 HTTP 500")},
		Probe: func(context.Context, *geoip2.Reader, asn.ASNResolver) []EntryOption {
			return nil
		},
	}
	model := NewModel(context.Background(), deps, DefaultSettings(baseFilter()), filepath.Join(t.TempDir(), "panel.json"))
	model.width, model.height = 120, 44
	model.settings.Mode = ModeCountry
	model.settings.Country = "DE"
	model.cursor = rowMode

	model = press(t, model, "s")
	if model.runner == nil {
		t.Fatalf("一键扫描应创建运行器")
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		phase := model.Status().Phase
		if phase == PhaseFailed || phase == PhaseDone || phase == PhaseStopped {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	status := model.Status()
	if status.Phase != PhaseFailed {
		t.Fatalf("国家清单查询失败应使扫描失败，实际阶段 %s", status.Phase)
	}
}

func TestModelProbeStatusMessages(t *testing.T) {
	model, _ := newTestModel(t, nil)

	// 仅 IPv4 可用：提示可继续
	updated, _ := model.Update(entriesProbedMsg{options: sampleEntries()})
	model = updated.(*Model)
	if !strings.Contains(viewText(model), "入口探测完成") {
		t.Fatalf("入口探测完成应有状态提示\n%s", viewText(model))
	}

	// 双栈都不可用：应引导改用国家模式
	unavailable := []EntryOption{
		entryOption(network.FamilyIPv4, "", 0, nil, false, "超时"),
		entryOption(network.FamilyIPv6, "", 0, nil, false, "无 IPv6 路由"),
	}
	updated, _ = model.Update(entriesProbedMsg{options: unavailable})
	model = updated.(*Model)
	if !strings.Contains(viewText(model), "改用「按国家/地区 ASN」") {
		t.Fatalf("双栈不可用时应引导改用国家模式\n%s", viewText(model))
	}
}

func TestModelResultsScrollingFocus(t *testing.T) {
	model, _ := newTestModel(t, sampleEntries())
	for i := 0; i < 6; i++ {
		model.rows = append(model.rows, Row{Domain: "host.example", IP: "198.51.100.7", Stars: 5, HandshakeMS: 120, CertDays: 90})
	}

	model = press(t, model, "tab")
	if model.focus != FocusResults {
		t.Fatalf("tab 应把焦点切到结果区")
	}
	model = press(t, model, "down", "down")
	if model.resultOffset != 2 {
		t.Fatalf("结果区应可滚动，实际偏移 %d", model.resultOffset)
	}
	model = press(t, model, "up", "up", "up", "up", "up")
	if model.resultOffset != 0 {
		t.Fatalf("结果区滚动不应越界，实际偏移 %d", model.resultOffset)
	}
	model = press(t, model, "tab")
	if model.focus != FocusSettings {
		t.Fatalf("tab 应切回参数区")
	}
}

func TestModelQuitKey(t *testing.T) {
	model, _ := newTestModel(t, sampleEntries())
	model = press(t, model, "q")
	if !strings.Contains(viewText(model), "面板已退出") {
		t.Fatalf("q 应退出面板\n%s", viewText(model))
	}
}

func TestModelSettingsPersistPath(t *testing.T) {
	model, settingsPath := newTestModel(t, sampleEntries())
	model.cursor = rowThreads
	model = press(t, model, "right")
	if model.SettingsPath() != settingsPath {
		t.Fatalf("设置路径应为注入的临时路径")
	}
	if !strings.Contains(model.SettingsNote(), settingsPath) {
		t.Fatalf("应提示设置已保存到 %s，实际 %q", settingsPath, model.SettingsNote())
	}
}

func TestModelIPv6VerificationUsesVPSCountry(t *testing.T) {
	entries := []EntryOption{
		entryOption(network.FamilyIPv4, "198.51.100.7", 64500, []string{"198.51.100.0/24"}, true, ""),
		entryOption(network.FamilyIPv6, "2001:db8::7", 64501, []string{"203.0.113.0/24"}, true, ""),
	}
	entries[0].CountryCode = "US"
	entries[1].CountryCode = "DE"

	deps := Deps{
		BaseCfg: types.Config{Filter: baseFilter()},
		Engine:  &core.Engine{},
		Scanner: &scanner.Scanner{},
		Country: &fakeCountryClient{resources: &asn.CountryResources{ASNs: []int{}}},
		Probe: func(context.Context, *geoip2.Reader, asn.ASNResolver) []EntryOption {
			return entries
		},
	}
	model := NewModel(context.Background(), deps, DefaultSettings(baseFilter()), filepath.Join(t.TempDir(), "panel.json"))
	model.width, model.height = 120, 44
	model.SetEntries(entries)
	model.settings.Family = FamilyV6

	if !strings.Contains(viewText(model), "VPS 所在国家 DE") {
		t.Fatalf("仅 IPv6 校验时应展示自动判定的 VPS 所在国家\n%s", viewText(model))
	}

	model = press(t, model, "s")
	countryPlan, ok := model.plan.(*CountryPlan)
	if !ok {
		t.Fatalf("仅 IPv6 校验应生成国家 ASN 计划，实际 %T", model.plan)
	}
	if countryPlan.country != "DE" || countryPlan.queueName != QueueVPSCountryASN {
		t.Fatalf("发现源应为 VPS 所在国家 DE，实际 %q / %q", countryPlan.country, countryPlan.queueName)
	}
}

func TestModelIPv6VerificationBlockedWithoutEgress(t *testing.T) {
	entries := []EntryOption{
		entryOption(network.FamilyIPv4, "198.51.100.7", 64500, []string{"198.51.100.0/24"}, true, ""),
		entryOption(network.FamilyIPv6, "", 0, nil, false, "无 IPv6 出口"),
	}
	deps := Deps{
		BaseCfg: types.Config{Filter: baseFilter()},
		Engine:  &core.Engine{},
		Scanner: &scanner.Scanner{},
		Country: &fakeCountryClient{resources: &asn.CountryResources{ASNs: []int{}}},
		Probe: func(context.Context, *geoip2.Reader, asn.ASNResolver) []EntryOption {
			return entries
		},
	}
	model := NewModel(context.Background(), deps, DefaultSettings(baseFilter()), filepath.Join(t.TempDir(), "panel.json"))
	model.width, model.height = 120, 44
	model.SetEntries(entries)
	model.settings.Family = FamilyV6

	model = press(t, model, "s")
	if model.runner != nil {
		t.Fatalf("本机无 IPv6 出口时不应启动仅 IPv6 校验")
	}
	if !strings.Contains(viewText(model), "IPv6 出口不可用") {
		t.Fatalf("应提示 IPv6 出口不可用\n%s", viewText(model))
	}
}
