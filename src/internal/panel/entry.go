package panel

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"RealityChecker/internal/asn"
	"RealityChecker/internal/network"

	"github.com/oschwald/geoip2-golang"
)

// EntryOption 一个可用的 VPS 出口（入口 IP）候选
type EntryOption struct {
	Family      network.Family `json:"family"`
	IP          string         `json:"ip"`
	ASN         int            `json:"asn"`
	Provider    string         `json:"provider,omitempty"`
	Kind        string         `json:"kind,omitempty"`
	CountryCode string         `json:"country_code,omitempty"`
	CountryName string         `json:"country_name,omitempty"`
	Source      string         `json:"source,omitempty"`
	Prefixes    []string       `json:"prefixes,omitempty"`
	Available   bool           `json:"available"`
	Err         string         `json:"error,omitempty"`
}

// ASNLabel 返回 ASN 的显示名
func (e EntryOption) ASNLabel() string {
	if e.ASN <= 0 {
		return "未知 ASN"
	}
	return fmt.Sprintf("AS%d", e.ASN)
}

// Summary 返回入口候选的一行摘要
func (e EntryOption) Summary() string {
	if !e.Available {
		reason := e.Err
		if reason == "" {
			reason = "不可用"
		}
		return fmt.Sprintf("%s 不可用（%s）", e.Family, reason)
	}
	parts := []string{e.IP, e.ASNLabel()}
	if e.CountryCode != "" && e.CountryCode != "UNKNOWN" {
		parts = append(parts, e.CountryCode)
	}
	if e.Kind != "" {
		parts = append(parts, e.Kind)
	}
	line := fmt.Sprintf("%s %s", e.Family, strings.Join(parts, " / "))
	if n := len(e.Prefixes); n > 0 {
		line += fmt.Sprintf("，%d 个网段", n)
	}
	return line
}

// EntryProbeFunc 入口探测实现，便于测试注入
type EntryProbeFunc func(ctx context.Context, reader *geoip2.Reader, resolver asn.ASNResolver) []EntryOption

// ProbeEntries 分别探测本机 IPv4 与 IPv6 出口并解析各自 ASN
func ProbeEntries(ctx context.Context, reader *geoip2.Reader, resolver asn.ASNResolver) []EntryOption {
	families := []network.Family{network.FamilyIPv4, network.FamilyIPv6}
	options := make([]EntryOption, len(families))
	var wg sync.WaitGroup
	for i, family := range families {
		wg.Add(1)
		go func(idx int, fam network.Family) {
			defer wg.Done()
			options[idx] = probeSingleEntry(ctx, reader, resolver, fam)
		}(i, family)
	}
	wg.Wait()
	return options
}

// probeSingleEntry 探测单个协议族的出口与 ASN
func probeSingleEntry(ctx context.Context, reader *geoip2.Reader, resolver asn.ASNResolver, family network.Family) EntryOption {
	option := EntryOption{Family: family}

	info, err := network.CheckEgressByFamily(ctx, family, reader)
	if err != nil {
		option.Err = shortError(err)
		return option
	}

	option.Available = true
	option.IP = info.IP
	option.CountryCode = info.CountryCode
	option.CountryName = info.CountryName
	option.Source = info.Source

	if resolver == nil {
		option.Err = "ASN 解析器未初始化"
		return option
	}

	asnNumber, prefixes, err := resolver.PrefixesForIP(ctx, info.IP)
	if err != nil {
		option.Err = fmt.Sprintf("ASN 解析失败：%s", shortError(err))
		return option
	}
	option.ASN = asnNumber
	option.Prefixes = prefixes
	option.Provider, option.Kind = describeASN(asnNumber)
	return option
}

// describeASN 返回 ASN 的运营商与网络类型描述
func describeASN(asnNumber int) (provider string, kind string) {
	if asnNumber <= 0 {
		return "", ""
	}
	if isCDN, name := asn.IsPureCDNASN(asnNumber); isCDN {
		return name, "CDN"
	}
	_, isResidential, name := asn.ClassifyASN(asnNumber, "")
	if isResidential {
		return name, "家宽"
	}
	return name, "机房"
}

// shortError 压缩错误信息，避免 TUI 里单行过长
func shortError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	msg = strings.ReplaceAll(msg, "\n", " ")
	if len(msg) > 90 {
		msg = msg[:90] + "…"
	}
	return msg
}
