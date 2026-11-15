package panel

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"RealityChecker/internal/asn"
	"RealityChecker/internal/network"
)

// fakeCountryClient 实现 CountryClient，用于离线验证国家扫描计划的编排逻辑
type fakeCountryClient struct {
	resources    *asn.CountryResources
	countryErr   error
	prefixes     map[int][]string
	fetchErrors  map[int]error
	fetchCalls   []string
	countryCalls []string
}

func (f *fakeCountryClient) FetchCountryResources(_ context.Context, country string) (*asn.CountryResources, error) {
	f.countryCalls = append(f.countryCalls, country)
	if f.countryErr != nil {
		return nil, f.countryErr
	}
	res := *f.resources
	res.Country = strings.ToUpper(country)
	return &res, nil
}

func (f *fakeCountryClient) FetchPrefixes(_ context.Context, resource string) ([]string, error) {
	f.fetchCalls = append(f.fetchCalls, resource)
	number, _ := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(resource), "AS"))
	if err := f.fetchErrors[number]; err != nil {
		return nil, err
	}
	return f.prefixes[number], nil
}

func entryOption(family network.Family, ip string, asnNumber int, prefixes []string, available bool, errText string) EntryOption {
	return EntryOption{
		Family:    family,
		IP:        ip,
		ASN:       asnNumber,
		Prefixes:  prefixes,
		Available: available,
		Err:       errText,
	}
}

func TestScanablePrefixesKeepsOnlyEnumerableIPv4(t *testing.T) {
	cidrs := []string{"198.51.100.0/24", "10.0.0.0/8", "2001:db8::/32", "2001:db8::/126", "198.51.100.7", "2001:db8::7", " "}
	scanable, skipped := scanablePrefixes(cidrs)

	if len(scanable) != 2 {
		t.Fatalf("发现阶段应只保留可枚举的 IPv4 目标，实际 %v", scanable)
	}
	for _, want := range []string{"198.51.100.0/24", "198.51.100.7"} {
		if !containsString(scanable, want) {
			t.Fatalf("可枚举网段缺少 %s：%v", want, scanable)
		}
	}
	if len(skipped) != 4 {
		t.Fatalf("应记录 4 个被跳过项（聚合大段 + IPv6），实际 %v", skipped)
	}
	reasons := strings.Join(skipped, " | ")
	if !strings.Contains(reasons, "无法逐 IP 枚举") {
		t.Fatalf("聚合大段应说明不可枚举：%s", reasons)
	}
	if !strings.Contains(reasons, "IPv6 网段不参与发现") || !strings.Contains(reasons, "IPv6 地址不参与发现") {
		t.Fatalf("IPv6 网段与地址应说明只做域名校验：%s", reasons)
	}
}

func TestEntryPlanUsesIPv4PrefixesForBothEntries(t *testing.T) {
	options := []EntryOption{
		entryOption(network.FamilyIPv4, "198.51.100.7", 64500, []string{"198.51.100.0/24", "2001:db8::/126"}, true, ""),
		entryOption(network.FamilyIPv6, "2001:db8::7", 64501, []string{"203.0.113.0/24"}, true, ""),
	}
	plan := NewEntryPlan(options, FamilyBoth)
	if err := plan.Prepare(context.Background()); err != nil {
		t.Fatalf("双栈入口准备失败: %v", err)
	}
	if plan.Stats().BatchesTotal != 2 {
		t.Fatalf("双栈应产生 2 个批次，实际 %d", plan.Stats().BatchesTotal)
	}

	first, err := plan.Next(context.Background())
	if err != nil || first == nil {
		t.Fatalf("第一个批次应存在: %v", err)
	}
	if first.ASN != 64500 || len(first.CIDRs) != 1 || first.CIDRs[0] != "198.51.100.0/24" {
		t.Fatalf("第一个批次应为入口 ASN 的 IPv4 网段，实际 %+v", first)
	}
	second, err := plan.Next(context.Background())
	if err != nil || second == nil || second.ASN != 64501 {
		t.Fatalf("第二个批次应为 IPv6 入口的 ASN: %v %+v", err, second)
	}
	if len(second.CIDRs) != 1 || second.CIDRs[0] != "203.0.113.0/24" {
		t.Fatalf("IPv6 入口同样用该 ASN 的 IPv4 网段发现，实际 %v", second.CIDRs)
	}
	if !strings.Contains(plan.Stats().Note, "IPv6 网段不参与发现") {
		t.Fatalf("应提示被跳过的 IPv6 网段，实际 %q", plan.Stats().Note)
	}
	if third, err := plan.Next(context.Background()); err != nil || third != nil {
		t.Fatalf("队列结束后应返回 nil: %v %+v", err, third)
	}
}

func TestEntryPlanDeduplicatesSameASN(t *testing.T) {
	options := []EntryOption{
		entryOption(network.FamilyIPv4, "198.51.100.7", 64500, []string{"198.51.100.0/24"}, true, ""),
		entryOption(network.FamilyIPv6, "2001:db8::7", 64500, []string{"198.51.100.0/24"}, true, ""),
	}
	plan := NewEntryPlan(options, FamilyBoth)
	if err := plan.Prepare(context.Background()); err != nil {
		t.Fatalf("准备失败: %v", err)
	}
	if plan.Stats().BatchesTotal != 1 {
		t.Fatalf("同一 ASN 的 v4/v6 入口应去重为 1 个批次，实际 %d", plan.Stats().BatchesTotal)
	}
	if !strings.Contains(plan.Stats().Note, "已跳过重复 ASN 入口") {
		t.Fatalf("应说明重复入口被跳过，实际 %q", plan.Stats().Note)
	}
}

func TestEntryPlanOnlyV4Selected(t *testing.T) {
	options := []EntryOption{
		entryOption(network.FamilyIPv4, "198.51.100.7", 64500, []string{"198.51.100.0/24"}, true, ""),
		entryOption(network.FamilyIPv6, "2001:db8::7", 64501, []string{"203.0.113.0/24"}, true, ""),
	}
	plan := NewEntryPlan(options, FamilyV4)
	if err := plan.Prepare(context.Background()); err != nil {
		t.Fatalf("仅 IPv4 选择准备失败: %v", err)
	}
	if plan.Stats().BatchesTotal != 1 {
		t.Fatalf("仅 IPv4 应只产生 1 个批次，实际 %d", plan.Stats().BatchesTotal)
	}
	batch, err := plan.Next(context.Background())
	if err != nil || batch == nil || batch.ASN != 64500 {
		t.Fatalf("应选中 IPv4 入口: %v %+v", err, batch)
	}
}

func TestEntryPlanV6Unavailable(t *testing.T) {
	options := []EntryOption{
		entryOption(network.FamilyIPv4, "198.51.100.7", 64500, []string{"198.51.100.0/24"}, true, ""),
		entryOption(network.FamilyIPv6, "", 0, nil, false, "IPv6 出口不可用"),
	}
	plan := NewEntryPlan(options, FamilyV6)
	err := plan.Prepare(context.Background())
	if err == nil {
		t.Fatalf("仅 IPv6 且 IPv6 不可用时应报错")
	}
	if !strings.Contains(err.Error(), "IPv6") || !strings.Contains(err.Error(), "不可用") {
		t.Fatalf("错误信息应指出 IPv6 不可用，实际 %q", err.Error())
	}
}

func TestEntryPlanAllUnavailable(t *testing.T) {
	options := []EntryOption{
		entryOption(network.FamilyIPv4, "", 0, nil, false, "超时"),
		entryOption(network.FamilyIPv6, "", 0, nil, false, "无 IPv6 路由"),
	}
	plan := NewEntryPlan(options, FamilyBoth)
	err := plan.Prepare(context.Background())
	if err == nil || !strings.Contains(err.Error(), "没有可用的入口 IP") {
		t.Fatalf("全部不可用时应给出明确错误，实际 %v", err)
	}
}

func TestEntryPlanRejectsEntryWithoutScannablePrefix(t *testing.T) {
	options := []EntryOption{
		entryOption(network.FamilyIPv4, "198.51.100.7", 64500, []string{"10.0.0.0/8"}, true, ""),
	}
	plan := NewEntryPlan(options, FamilyV4)
	err := plan.Prepare(context.Background())
	if err == nil {
		t.Fatalf("入口只公告聚合大段时应直接报错，而不是静默扫 0 个 IP")
	}
	if !strings.Contains(err.Error(), "无法逐 IP 枚举") {
		t.Fatalf("错误信息应说明不可枚举，实际 %q", err.Error())
	}
}

func TestEntryPlanSkipsIPv6OnlyASNAndKeepsNotes(t *testing.T) {
	options := []EntryOption{
		entryOption(network.FamilyIPv4, "198.51.100.7", 64500, []string{"198.51.100.0/24"}, true, ""),
		entryOption(network.FamilyIPv6, "2001:db8::7", 64501, []string{"2001:db8::/32"}, true, ""),
	}
	plan := NewEntryPlan(options, FamilyBoth)
	if err := plan.Prepare(context.Background()); err != nil {
		t.Fatalf("只有 IPv6 网段的入口不应阻塞可扫描的 IPv4 入口: %v", err)
	}
	if plan.Stats().BatchesTotal != 1 {
		t.Fatalf("应只保留可枚举的入口，实际 %d", plan.Stats().BatchesTotal)
	}
	if !strings.Contains(plan.Stats().Note, "已跳过不可用出口") {
		t.Fatalf("应说明被跳过的入口原因，实际 %q", plan.Stats().Note)
	}
}

func TestCountryPlanRequiresValidCountry(t *testing.T) {
	client := &fakeCountryClient{resources: &asn.CountryResources{ASNs: []int{64500}}}
	for _, country := range []string{"", "DEU", "1X"} {
		plan := NewCountryPlan(client, country, "")
		if err := plan.Prepare(context.Background()); err == nil {
			t.Fatalf("非法国家代码 %q 应被拒绝", country)
		}
	}
}

func TestCountryPlanPrepareUsesOnlineList(t *testing.T) {
	client := &fakeCountryClient{resources: &asn.CountryResources{
		ASNs:      []int{64500, 64501},
		IPv4:      []string{"198.51.100.0/24"},
		IPv6:      []string{"2001:db8::/32"},
		QueryTime: "2026-01-01T00:00:00",
	}}
	plan := NewCountryPlan(client, "de", "")
	if err := plan.Prepare(context.Background()); err != nil {
		t.Fatalf("国家清单准备失败: %v", err)
	}
	stats := plan.Stats()
	if stats.Country != "DE" {
		t.Fatalf("国家代码应规范为大写，实际 %q", stats.Country)
	}
	if stats.CountryASNs != 2 || stats.CountryIPv4 != 1 || stats.CountryIPv6 != 1 {
		t.Fatalf("清单统计不正确: %+v", stats)
	}
	if stats.QueueName != QueueCountryASN {
		t.Fatalf("手填国家计划应使用默认队列名，实际 %q", stats.QueueName)
	}
	if len(client.countryCalls) != 1 || client.countryCalls[0] != "DE" {
		t.Fatalf("应向在线接口查询一次该国清单，实际 %v", client.countryCalls)
	}
}

func TestCountryPlanIteratesASNAndSkipsIPv6Only(t *testing.T) {
	client := &fakeCountryClient{
		resources: &asn.CountryResources{ASNs: []int{64500, 64501, 64502}},
		prefixes: map[int][]string{
			64500: {"198.51.100.0/24"},
			64501: {"2001:db8::/126"},
			64502: {"203.0.113.0/24"},
		},
		fetchErrors: map[int]error{},
	}
	plan := NewCountryPlan(client, "DE", QueueVPSCountryASN)
	if err := plan.Prepare(context.Background()); err != nil {
		t.Fatalf("准备失败: %v", err)
	}
	if !strings.Contains(plan.Summary(), "VPS 所在国家") {
		t.Fatalf("VPS 所在国家计划应有明确描述: %s", plan.Summary())
	}
	if stats := plan.Stats(); stats.QueueName != QueueVPSCountryASN {
		t.Fatalf("队列名应为 %s，实际 %q", QueueVPSCountryASN, stats.QueueName)
	}

	batch, err := plan.Next(context.Background())
	if err != nil {
		t.Fatalf("取批次失败: %v", err)
	}
	if batch == nil || batch.ASN != 64500 || len(batch.CIDRs) != 1 || batch.CIDRs[0] != "198.51.100.0/24" {
		t.Fatalf("发现阶段应只取 IPv4 网段，实际 %+v", batch)
	}
	next, err := plan.Next(context.Background())
	if err != nil {
		t.Fatalf("取批次失败: %v", err)
	}
	if next == nil || next.ASN != 64502 {
		t.Fatalf("只公告 IPv6 的 ASN 应被跳过，实际 %+v", next)
	}
	if stats := plan.Stats(); !strings.Contains(strings.Join(stats.Skipped, " | "), "IPv6") {
		t.Fatalf("应记录被跳过的 IPv6 段，实际 %+v", stats.Skipped)
	}
	if last, err := plan.Next(context.Background()); err != nil || last != nil {
		t.Fatalf("队列应已结束: %v %+v", err, last)
	}
}

func TestCountryPlanSkipsFetchErrors(t *testing.T) {
	client := &fakeCountryClient{
		resources: &asn.CountryResources{ASNs: []int{64500, 64501}},
		prefixes: map[int][]string{
			64501: {"198.51.100.0/24"},
		},
		fetchErrors: map[int]error{64500: fmt.Errorf("HTTP 500")},
	}
	plan := NewCountryPlan(client, "JP", "")
	if err := plan.Prepare(context.Background()); err != nil {
		t.Fatalf("准备失败: %v", err)
	}
	batch, err := plan.Next(context.Background())
	if err != nil {
		t.Fatalf("查询失败应被跳过而不是中断整轮扫描: %v", err)
	}
	if batch == nil || batch.ASN != 64501 {
		t.Fatalf("应继续扫描后续 ASN，实际 %+v", batch)
	}
	stats := plan.Stats()
	if len(stats.Skipped) != 1 || !strings.Contains(stats.Skipped[0], "AS64500") {
		t.Fatalf("应记录被跳过的 ASN，实际 %+v", stats.Skipped)
	}
	if len(client.fetchCalls) != 2 {
		t.Fatalf("应按顺序查询两个 ASN，实际 %v", client.fetchCalls)
	}
}

func TestCountryPlanStopsOnContextCancel(t *testing.T) {
	client := &fakeCountryClient{
		resources:   &asn.CountryResources{ASNs: []int{64500, 64501}},
		prefixes:    map[int][]string{64500: {"198.51.100.0/24"}},
		fetchErrors: map[int]error{},
	}
	plan := NewCountryPlan(client, "DE", "")
	if err := plan.Prepare(context.Background()); err != nil {
		t.Fatalf("准备失败: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := plan.Next(ctx); err == nil {
		t.Fatalf("上下文取消后应返回错误以结束队列")
	}
}

func TestEntrySummaryMentionsFamilyAndReason(t *testing.T) {
	available := entryOption(network.FamilyIPv4, "198.51.100.7", 64500, []string{"198.51.100.0/24"}, true, "")
	available.CountryCode = "DE"
	available.Kind = "机房"
	line := available.Summary()
	for _, want := range []string{"IPv4", "198.51.100.7", "AS64500", "DE", "机房", "1 个网段"} {
		if !strings.Contains(line, want) {
			t.Fatalf("入口摘要缺少 %q：%s", want, line)
		}
	}
	unavailable := entryOption(network.FamilyIPv6, "", 0, nil, false, "无 IPv6 出口")
	if !strings.Contains(unavailable.Summary(), "不可用") {
		t.Fatalf("不可用入口应明确标注：%s", unavailable.Summary())
	}
}

// containsString 判断字符串切片是否包含目标值
func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestEntryPlanNotesSkippedAggregates(t *testing.T) {
	options := []EntryOption{
		entryOption(network.FamilyIPv4, "198.51.100.7", 64500, []string{"10.0.0.0/8", "198.51.100.0/24"}, true, ""),
	}
	plan := NewEntryPlan(options, FamilyV4)
	if err := plan.Prepare(context.Background()); err != nil {
		t.Fatalf("混合网段时应保留可枚举部分: %v", err)
	}
	if !strings.Contains(plan.Stats().Note, "已跳过不可枚举网段") {
		t.Fatalf("应提示被跳过的聚合大段，实际 %q", plan.Stats().Note)
	}
	batch, err := plan.Next(context.Background())
	if err != nil || batch == nil {
		t.Fatalf("取批次失败: %v", err)
	}
	if len(batch.CIDRs) != 1 || batch.CIDRs[0] != "198.51.100.0/24" {
		t.Fatalf("批次应只含可枚举网段，实际 %v", batch.CIDRs)
	}
}
