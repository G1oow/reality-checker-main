package panel

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"RealityChecker/internal/asn"
	"RealityChecker/internal/network"

	"github.com/oschwald/geoip2-golang"
)

// newTestMenu 构造菜单：探测结果固定为「可用 IPv4 出口 DE + 不可用 IPv6 出口」
func newTestMenu(t *testing.T, input string, settings Settings) (*Menu, *bytes.Buffer, string) {
	t.Helper()
	out := &bytes.Buffer{}
	settingsPath := filepath.Join(t.TempDir(), "panel.json")
	color := false
	menu := NewMenu(context.Background(), MenuConfig{
		Deps: Deps{
			Probe: func(context.Context, *geoip2.Reader, asn.ASNResolver) []EntryOption {
				return []EntryOption{
					countryEntry(network.FamilyIPv4, true, "DE"),
					countryEntry(network.FamilyIPv6, false, ""),
				}
			},
		},
		Settings:     settings,
		SettingsPath: settingsPath,
		In:           strings.NewReader(input),
		Out:          out,
		Color:        &color,
	})
	return menu, out, settingsPath
}

func runTestMenu(t *testing.T, menu *Menu) string {
	t.Helper()
	if err := menu.Run(); err != nil {
		t.Fatalf("菜单运行失败: %v", err)
	}
	return StripANSI(menuOutput(menu, t))
}

// menuOutput 从菜单的输出缓冲里取文本（newTestMenu 返回的缓冲由调用方保留）
func menuOutput(menu *Menu, t *testing.T) string {
	t.Helper()
	buffer, ok := menu.out.(*bytes.Buffer)
	if !ok {
		t.Fatalf("菜单输出不是 *bytes.Buffer")
	}
	return buffer.String()
}

func TestMenuRendersGroupedNumberedItems(t *testing.T) {
	menu, _, _ := newTestMenu(t, "0\n", DefaultSettings(baseFilter()))
	text := runTestMenu(t, menu)

	for _, want := range []string{
		"Reality Checker",
		"本机出口",
		"发现来源",
		"【扫描参数】",
		"【过滤策略】",
		"【执行】",
		"[1]", "[13]",
		"请输入选项",
		"已退出面板",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("菜单输出缺少 %q：\n%s", want, text)
		}
	}
}

func TestMenuEditLimitSavesAndNotifies(t *testing.T) {
	menu, _, settingsPath := newTestMenu(t, "4\n3\n0\n", DefaultSettings(baseFilter()))
	text := runTestMenu(t, menu)

	if got := menu.Settings().Limit; got != 3 {
		t.Fatalf("目标数量应为 3，实际 %d", got)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("读取设置文件失败: %v", err)
	}
	if !strings.Contains(string(data), "\"limit\": 3") {
		t.Fatalf("设置文件未写入 limit=3：\n%s", data)
	}
	if !strings.Contains(text, "目标数量已设为 3") {
		t.Fatalf("缺少保存提示：\n%s", text)
	}
}

func TestMenuRejectsInvalidChoiceAndKeepsRunning(t *testing.T) {
	menu, _, _ := newTestMenu(t, "99\nabc\n0\n", DefaultSettings(baseFilter()))
	text := runTestMenu(t, menu)

	if count := strings.Count(text, "无效输入"); count < 2 {
		t.Fatalf("两次无效输入都应提示，实际 %d 次：\n%s", count, text)
	}
	if menu.Settings().Limit != DefaultSettings(baseFilter()).Limit {
		t.Fatalf("无效输入不应改动参数：%+v", menu.Settings())
	}
}

func TestMenuSubmenuRetriesOutOfRangeNumber(t *testing.T) {
	menu, _, _ := newTestMenu(t, "2\n9\n1\n0\n", DefaultSettings(baseFilter()))
	text := runTestMenu(t, menu)

	if !strings.Contains(text, "编号无效") {
		t.Fatalf("越界编号应提示：\n%s", text)
	}
	if got := menu.Settings().Family; got != FamilyV4 {
		t.Fatalf("校验协议族应为仅 IPv4，实际 %q", got)
	}
}

func TestMenuCountryCodeRejectsInvalidInput(t *testing.T) {
	menu, _, _ := newTestMenu(t, "3\nXX1\nDE\n0\n", DefaultSettings(baseFilter()))
	text := runTestMenu(t, menu)

	if !strings.Contains(text, "代码无效") {
		t.Fatalf("非法国家代码应提示：\n%s", text)
	}
	if got := menu.Settings().Country; got != "DE" {
		t.Fatalf("国家代码应为 DE，实际 %q", got)
	}
}

func TestMenuCountryCodeClearsWithDash(t *testing.T) {
	settings := DefaultSettings(baseFilter())
	settings.Country = "JP"
	menu, _, _ := newTestMenu(t, "3\n-\n0\n", settings)
	runTestMenu(t, menu)

	if got := menu.Settings().Country; got != "" {
		t.Fatalf("输入 - 应清空国家代码，实际 %q", got)
	}
}

func TestMenuToggleFilterSwitches(t *testing.T) {
	// [10] 允许 CDN 默认否（base 里 RequireNoCDN=true），选 1 打开
	menu, _, _ := newTestMenu(t, "10\n1\n12\n2\n0\n", DefaultSettings(baseFilter()))
	runTestMenu(t, menu)

	if !menu.Settings().Filter.AllowCDN {
		t.Fatalf("允许 CDN 应被打开：%+v", menu.Settings().Filter)
	}
	if menu.Settings().Filter.ExcludeCN {
		t.Fatalf("排除国内站点应被关闭：%+v", menu.Settings().Filter)
	}
}

func TestMenuIPv6OnlyLocksDiscoveryMode(t *testing.T) {
	settings := DefaultSettings(baseFilter())
	settings.Family = FamilyV6
	menu, _, _ := newTestMenu(t, "1\n0\n", settings)
	text := runTestMenu(t, menu)

	if !strings.Contains(text, "固定") {
		t.Fatalf("仅 IPv6 时应提示发现源固定：\n%s", text)
	}
	if got := menu.Settings().Family; got != FamilyV6 {
		t.Fatalf("协议族不应被改动，实际 %q", got)
	}
}

func TestMenuScanWithoutEngineReportsError(t *testing.T) {
	menu, _, _ := newTestMenu(t, "s\n0\n", DefaultSettings(baseFilter()))
	text := runTestMenu(t, menu)

	if !strings.Contains(text, "扫描引擎未初始化") {
		t.Fatalf("缺少引擎时应提示原因：\n%s", text)
	}
}

func TestMenuShowsLastScanResults(t *testing.T) {
	menu, _, _ := newTestMenu(t, "d\n\n0\n", DefaultSettings(baseFilter()))
	menu.status = Status{Phase: PhaseDone, Message: "命中 1 个目标"}
	menu.rows = []Row{{
		Domain:      "example.com",
		IP:          "198.51.100.7",
		ASN:         "AS64500",
		Country:     "DE",
		Stars:       5,
		HandshakeMS: 123,
		CertDays:    88,
		StatusCode:  200,
	}}
	text := runTestMenu(t, menu)

	for _, want := range []string{"【上次扫描结果】", "域名", "example.com", "198.51.100.7", "已退出面板"} {
		if !strings.Contains(text, want) {
			t.Fatalf("结果表缺少 %q：\n%s", want, text)
		}
	}
}

func TestMenuEmptyResultsExplainsWhy(t *testing.T) {
	menu, _, _ := newTestMenu(t, "d\n\n0\n", DefaultSettings(baseFilter()))
	menu.status = Status{Phase: PhaseDone, Message: "ASN 队列已扫描完毕"}
	text := runTestMenu(t, menu)

	if !strings.Contains(text, "没有命中") {
		t.Fatalf("空结果应给出说明：\n%s", text)
	}
}

func TestMenuExitsOnEOF(t *testing.T) {
	menu, _, _ := newTestMenu(t, "", DefaultSettings(baseFilter()))
	text := runTestMenu(t, menu)

	if !strings.Contains(text, "已退出面板") {
		t.Fatalf("EOF 后应安全退出：\n%s", text)
	}
}

func TestMenuBannerIsAligned(t *testing.T) {
	menu, _, _ := newTestMenu(t, "0\n", DefaultSettings(baseFilter()))
	menu.color = false
	lines := strings.Split(StripANSI(menu.renderBanner()), "\n")
	widths := make(map[int]struct{})
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		widths[displayWidth(line)] = struct{}{}
	}
	if len(widths) != 1 {
		t.Fatalf("横幅各行宽度不一致：%v", widths)
	}
}
