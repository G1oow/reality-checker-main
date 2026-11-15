package panel

import (
	"testing"

	"RealityChecker/internal/network"
)

func countryEntry(family network.Family, available bool, countryCode string) EntryOption {
	option := entryOption(family, "198.51.100.7", 64500, []string{"198.51.100.0/24"}, available, "")
	if family == network.FamilyIPv6 {
		option.IP = "2001:db8::7"
	}
	option.CountryCode = countryCode
	return option
}

func TestAutoDiscoveryCountryPrefersIPv6Egress(t *testing.T) {
	options := []EntryOption{
		countryEntry(network.FamilyIPv4, true, "US"),
		countryEntry(network.FamilyIPv6, true, "DE"),
	}
	if got := AutoDiscoveryCountry(options); got != "DE" {
		t.Fatalf("应优先使用 IPv6 出口归属国家，实际 %q", got)
	}
}

func TestAutoDiscoveryCountryFallsBackToIPv4AndSkipsUnknown(t *testing.T) {
	options := []EntryOption{
		countryEntry(network.FamilyIPv4, true, "JP"),
		countryEntry(network.FamilyIPv6, false, ""),
	}
	if got := AutoDiscoveryCountry(options); got != "JP" {
		t.Fatalf("IPv6 出口不可用时应回退 IPv4 归属国家，实际 %q", got)
	}

	unknown := []EntryOption{
		countryEntry(network.FamilyIPv4, true, "UNKNOWN"),
		countryEntry(network.FamilyIPv6, true, ""),
	}
	if got := AutoDiscoveryCountry(unknown); got != "" {
		t.Fatalf("无法判定的归属地不应被当作国家代码，实际 %q", got)
	}
	if got := AutoDiscoveryCountry(nil); got != "" {
		t.Fatalf("无探测结果时应返回空串，实际 %q", got)
	}
}

func TestStartBlockerForIPv6Verification(t *testing.T) {
	settings := DefaultSettings(baseFilter())
	settings.Family = FamilyV6

	// 本机没有 IPv6 出口：仅 IPv6 校验必然零命中，直接阻塞
	noV6 := []EntryOption{countryEntry(network.FamilyIPv4, true, "US"), countryEntry(network.FamilyIPv6, false, "无 IPv6 出口")}
	if reason := StartBlocker(settings, noV6, false); reason == "" {
		t.Fatalf("IPv6 出口不可用时应阻塞仅 IPv6 校验")
	}

	// 有 IPv6 出口但归属地未知：需要手动填写国家代码
	noCountry := []EntryOption{countryEntry(network.FamilyIPv4, false, ""), countryEntry(network.FamilyIPv6, true, "UNKNOWN")}
	if reason := StartBlocker(settings, noCountry, false); reason == "" {
		t.Fatalf("无法判定 VPS 所在国家时应阻塞扫描")
	}
	settings.Country = "DE"
	if reason := StartBlocker(settings, noCountry, false); reason != "" {
		t.Fatalf("手动填写国家代码后应可扫描，实际 %q", reason)
	}

	// 双栈 / 仅 IPv4 校验不受这些限制
	settings.Family = FamilyBoth
	settings.Country = ""
	if reason := StartBlocker(settings, noV6, false); reason != "" {
		t.Fatalf("双栈校验不应被 IPv6 出口缺失阻塞，实际 %q", reason)
	}
	if reason := StartBlocker(settings, noV6, true); reason == "" {
		t.Fatalf("入口仍在探测时应提示等待")
	}
	countrySettings := DefaultSettings(baseFilter())
	countrySettings.Mode = ModeCountry
	countrySettings.Country = "DE"
	if reason := StartBlocker(countrySettings, nil, true); reason != "" {
		t.Fatalf("国家模式不依赖入口探测，不应阻塞，实际 %q", reason)
	}
}

func TestHasUsableIPv6Egress(t *testing.T) {
	if HasUsableIPv6Egress(nil) {
		t.Fatalf("无探测结果时不应视为有 IPv6 出口")
	}
	if HasUsableIPv6Egress([]EntryOption{countryEntry(network.FamilyIPv4, true, "US")}) {
		t.Fatalf("只有 IPv4 出口时不应视为有 IPv6 出口")
	}
	if !HasUsableIPv6Egress([]EntryOption{countryEntry(network.FamilyIPv6, true, "DE")}) {
		t.Fatalf("可用 IPv6 出口应被识别")
	}
}
