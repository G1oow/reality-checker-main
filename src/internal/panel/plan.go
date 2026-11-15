package panel

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"RealityChecker/internal/asn"
	"RealityChecker/internal/network"
)

// Batch 一批待扫描网段（一个 ASN 或一个入口 IP 所属 ASN 的公告网段）
type Batch struct {
	Label   string
	ASN     int
	Country string
	CIDRs   []string
}

// PlanStats 扫描计划的统计信息，供面板展示
type PlanStats struct {
	QueueName        string
	BatchesTotal     int
	BatchesDone      int
	Country          string
	CountryASNs      int
	CountryIPv4      int
	CountryIPv6      int
	CountryQueryTime string
	EntryOptions     []EntryOption
	Skipped          []string
	Note             string
}

// Plan 扫描计划：按需产出待扫描网段批次
type Plan interface {
	// Mode 返回计划对应的扫描模式
	Mode() Mode
	// Summary 返回一句话描述
	Summary() string
	// Prepare 完成计划所需的前置查询（国家 ASN 清单 / 入口筛选）
	Prepare(ctx context.Context) error
	// Next 返回下一批网段；返回 (nil, nil) 表示队列结束
	Next(ctx context.Context) (*Batch, error)
	// Stats 返回当前统计
	Stats() PlanStats
}

// PrefixFetcher 拉取单个 ASN 公告网段的能力
type PrefixFetcher interface {
	FetchPrefixes(ctx context.Context, resource string) ([]string, error)
}

// CountryASNFetcher 拉取某国 ASN 清单的能力
type CountryASNFetcher interface {
	FetchCountryResources(ctx context.Context, country string) (*asn.CountryResources, error)
}

// CountryClient 同时具备国家 ASN 清单与单个 ASN 网段查询能力
type CountryClient interface {
	CountryASNFetcher
	PrefixFetcher
}

// filterByFamily 按协议族筛选 CIDR
func filterByFamily(cidrs []string, family FamilyChoice) []string {
	switch family {
	case FamilyV4:
		return asn.FilterIPVersion(cidrs, true, false)
	case FamilyV6:
		return asn.FilterIPVersion(cidrs, false, true)
	default:
		return cidrs
	}
}

// 可逐 IP 枚举的网段规模上限（按主机位计算），超过则视为无法扫描的聚合大段
const (
	maxIPv4HostBits = 16 // /16 及更小网段可枚举
	maxIPv6HostBits = 16 // /112 及更小网段可枚举
)

// selectedEntry 已通过协议族与可枚举规模校验的入口
type selectedEntry struct {
	option EntryOption
	cidrs  []string
}

// scanablePrefixes 按协议族与可枚举规模筛选网段，返回可扫描网段与被跳过的网段说明
func scanablePrefixes(cidrs []string, family FamilyChoice) (scanable []string, skipped []string) {
	for _, raw := range filterByFamily(cidrs, family) {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			if _, addrErr := netip.ParseAddr(value); addrErr == nil {
				scanable = append(scanable, value)
				continue
			}
			skipped = append(skipped, fmt.Sprintf("%s（格式无法解析）", value))
			continue
		}
		limit := maxIPv4HostBits
		if !prefix.Addr().Is4() {
			limit = maxIPv6HostBits
		}
		hostBits := prefix.Addr().BitLen() - prefix.Bits()
		if hostBits > limit {
			skipped = append(skipped, fmt.Sprintf("%s（聚合段含 2^%d 个地址，无法逐 IP 枚举）", prefix.Masked().String(), hostBits))
			continue
		}
		scanable = append(scanable, prefix.Masked().String())
	}
	return scanable, skipped
}

// summarize 截断列表用于单行提示
func summarize(items []string, max int) []string {
	if max <= 0 || len(items) <= max {
		return items
	}
	out := make([]string, 0, max+1)
	out = append(out, items[:max]...)
	return append(out, fmt.Sprintf("等 %d 项", len(items)))
}

// EntryPlan 按 VPS 入口 IP 的 ASN 扫描
type EntryPlan struct {
	options  []EntryOption
	family   FamilyChoice
	selected []selectedEntry
	cursor   int
	stats    PlanStats
}

// NewEntryPlan 依据入口探测结果与协议族选择生成计划
func NewEntryPlan(options []EntryOption, family FamilyChoice) *EntryPlan {
	return &EntryPlan{
		options: options,
		family:  family,
		stats:   PlanStats{QueueName: "入口 ASN", EntryOptions: options},
	}
}

// Mode 返回扫描模式
func (p *EntryPlan) Mode() Mode { return ModeEntry }

// Summary 返回计划描述
func (p *EntryPlan) Summary() string {
	return "按 VPS 入口 IP 的 ASN 扫描"
}

// Prepare 筛选出符合协议族选择且可用的入口
func (p *EntryPlan) Prepare(_ context.Context) error {
	wanted := map[network.Family]bool{}
	for _, family := range (Settings{Family: p.family}).Families() {
		wanted[family] = true
	}

	unavailable := make([]string, 0, 2)
	oversize := make([]string, 0, 4)
	p.selected = p.selected[:0]
	for _, option := range p.options {
		if !wanted[option.Family] {
			continue
		}
		if !option.Available || option.ASN <= 0 || len(option.Prefixes) == 0 {
			reason := option.Err
			if reason == "" {
				reason = "不可用"
			}
			unavailable = append(unavailable, fmt.Sprintf("%s（%s）", option.Family, reason))
			continue
		}
		cidrs, skipped := scanablePrefixes(option.Prefixes, p.family)
		if len(cidrs) == 0 {
			reason := "该 ASN 没有可逐 IP 枚举的网段"
			if len(skipped) > 0 {
				reason = joinStrings(summarize(skipped, 2), "、")
			}
			unavailable = append(unavailable, fmt.Sprintf("%s %s（%s）", option.Family, option.ASNLabel(), reason))
			continue
		}
		oversize = append(oversize, skipped...)
		p.selected = append(p.selected, selectedEntry{option: option, cidrs: cidrs})
	}

	if len(p.selected) == 0 {
		if len(unavailable) > 0 {
			return fmt.Errorf("没有可用的入口 IP：%s", joinStrings(unavailable, "、"))
		}
		return fmt.Errorf("没有可用的入口 IP，请检查本机网络")
	}

	p.stats.BatchesTotal = len(p.selected)
	notes := make([]string, 0, 2)
	if len(unavailable) > 0 {
		notes = append(notes, "已跳过不可用出口："+joinStrings(unavailable, "、"))
	}
	if len(oversize) > 0 {
		notes = append(notes, "已跳过不可枚举网段："+joinStrings(summarize(oversize, 3), "、"))
	}
	p.stats.Note = joinStrings(notes, "；")
	return nil
}

// Next 返回下一个入口的公告网段
func (p *EntryPlan) Next(_ context.Context) (*Batch, error) {
	if p.cursor >= len(p.selected) {
		return nil, nil
	}
	entry := p.selected[p.cursor]
	p.cursor++
	p.stats.BatchesDone = p.cursor

	return &Batch{
		Label:   fmt.Sprintf("%s / %s", entry.option.Family, entry.option.ASNLabel()),
		ASN:     entry.option.ASN,
		Country: entry.option.CountryCode,
		CIDRs:   entry.cidrs,
	}, nil
}

// Stats 返回计划统计
func (p *EntryPlan) Stats() PlanStats { return p.stats }

// CountryPlan 按国家/地区的 ASN 清单依次扫描
type CountryPlan struct {
	fetcher CountryClient
	country string
	family  FamilyChoice

	resources *asn.CountryResources
	cursor    int
	skipped   []string
	stats     PlanStats
}

// NewCountryPlan 创建按国家扫描的计划（ASN 清单在 Prepare 阶段在线拉取）
func NewCountryPlan(fetcher CountryClient, country string, family FamilyChoice) *CountryPlan {
	return &CountryPlan{
		fetcher: fetcher,
		country: country,
		family:  family,
		stats:   PlanStats{QueueName: "国家 ASN 队列", Country: country},
	}
}

// Mode 返回扫描模式
func (p *CountryPlan) Mode() Mode { return ModeCountry }

// Summary 返回计划描述
func (p *CountryPlan) Summary() string {
	if p.resources == nil {
		return fmt.Sprintf("按国家/地区 ASN 扫描（%s）", p.country)
	}
	return fmt.Sprintf("按国家/地区 ASN 扫描（%s，%d 个 ASN）", p.country, len(p.resources.ASNs))
}

// Prepare 在线拉取该国 ASN 清单
func (p *CountryPlan) Prepare(ctx context.Context) error {
	if p.fetcher == nil {
		return fmt.Errorf("国家 ASN 查询客户端未初始化")
	}
	country := asn.NormalizeCountryCode(p.country)
	if country == "" {
		return fmt.Errorf("国家代码必须是两位 ISO alpha-2（如 DE、JP）")
	}
	p.country = country

	resources, err := p.fetcher.FetchCountryResources(ctx, country)
	if err != nil {
		return err
	}
	p.resources = resources
	p.stats.Country = resources.Country
	p.stats.CountryASNs = len(resources.ASNs)
	p.stats.CountryIPv4 = len(resources.IPv4)
	p.stats.CountryIPv6 = len(resources.IPv6)
	p.stats.CountryQueryTime = resources.QueryTime
	p.stats.BatchesTotal = len(resources.ASNs)
	return nil
}

// Next 依次取下一个 ASN 的公告网段（空网段与查询失败自动跳过）
func (p *CountryPlan) Next(ctx context.Context) (*Batch, error) {
	if p.resources == nil {
		return nil, fmt.Errorf("国家 ASN 清单尚未加载")
	}
	if p.fetcher == nil {
		return nil, fmt.Errorf("ASN 网段查询客户端未初始化")
	}

	for p.cursor < len(p.resources.ASNs) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		asnNumber := p.resources.ASNs[p.cursor]
		p.cursor++
		p.stats.BatchesDone = p.cursor

		resource := fmt.Sprintf("AS%d", asnNumber)
		prefixes, err := p.fetcher.FetchPrefixes(ctx, resource)
		if err != nil {
			p.recordSkip(fmt.Sprintf("%s（%s）", resource, shortError(err)))
			continue
		}
		cidrs, skipped := scanablePrefixes(prefixes, p.family)
		for _, reason := range skipped {
			p.recordSkip(reason)
		}
		if len(cidrs) == 0 {
			p.recordSkip(fmt.Sprintf("%s（无可逐 IP 枚举的网段）", resource))
			continue
		}
		return &Batch{
			Label:   resource,
			ASN:     asnNumber,
			Country: p.resources.Country,
			CIDRs:   cidrs,
		}, nil
	}
	return nil, nil
}

// Stats 返回计划统计
func (p *CountryPlan) Stats() PlanStats {
	p.stats.Skipped = p.skipped
	return p.stats
}

// recordSkip 记录被跳过的 ASN，最多保留最近 12 条
func (p *CountryPlan) recordSkip(reason string) {
	p.skipped = append(p.skipped, reason)
	if len(p.skipped) > 12 {
		p.skipped = p.skipped[len(p.skipped)-12:]
	}
}

// joinStrings 以分隔符拼接字符串切片
func joinStrings(items []string, sep string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += sep
		}
		out += item
	}
	return out
}
