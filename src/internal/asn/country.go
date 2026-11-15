package asn

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

// CountryResources 一个国家/地区已公告的 ASN 与网段清单（来自 RIPE Stat country-resource-list）
type CountryResources struct {
	Country   string   `json:"country"`
	ASNs      []int    `json:"asns"`
	IPv4      []string `json:"ipv4"`
	IPv6      []string `json:"ipv6"`
	QueryTime string   `json:"query_time"`
	Source    string   `json:"source"`
}

// IsCountryCode reports whether value is a two-letter ISO 3166-1 alpha-2 code.
func IsCountryCode(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 2 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

// NormalizeCountryCode 归一化为两位大写 ISO 代码；非法输入返回空串
func NormalizeCountryCode(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if !IsCountryCode(value) {
		return ""
	}
	return value
}

// FetchCountryResources queries RIPE Stat for the announced ASNs and prefixes of one country.
func (c *Client) FetchCountryResources(ctx context.Context, country string) (*CountryResources, error) {
	country = strings.ToUpper(strings.TrimSpace(country))
	if !IsCountryCode(country) {
		return nil, fmt.Errorf("国家代码必须是两位 ISO alpha-2（如 DE、JP）")
	}
	if c.countryEndpoint == "" {
		c.countryEndpoint = defaultCountryEndpoint
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.countryEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建国家资源请求失败: %w", err)
	}
	query := req.URL.Query()
	query.Set("resource", country)
	req.URL.RawQuery = query.Encode()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("查询 %s 的 ASN 清单失败: %w", country, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("查询 %s 的 ASN 清单失败: HTTP %s", country, resp.Status)
	}

	var payload struct {
		DataCallStatus string `json:"data_call_status"`
		Data           struct {
			QueryTime string `json:"query_time"`
			Resources struct {
				ASN  []string `json:"asn"`
				IPv4 []string `json:"ipv4"`
				IPv6 []string `json:"ipv6"`
			} `json:"resources"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析 %s 的 ASN 清单失败: %w", country, err)
	}
	if payload.DataCallStatus != "" && payload.DataCallStatus != "supported" {
		return nil, fmt.Errorf("RIPE Stat 未支持该查询（%s）", payload.DataCallStatus)
	}

	res := &CountryResources{
		Country:   country,
		QueryTime: payload.Data.QueryTime,
		Source:    "stat.ripe.net/country-resource-list",
	}
	seen := make(map[int]struct{}, len(payload.Data.Resources.ASN))
	for _, raw := range payload.Data.Resources.ASN {
		value := 0
		if _, err := fmt.Sscanf(strings.TrimSpace(raw), "%d", &value); err != nil || value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		res.ASNs = append(res.ASNs, value)
	}
	sort.Ints(res.ASNs)
	res.IPv4 = normalizePrefixes(payload.Data.Resources.IPv4)
	res.IPv6 = normalizePrefixes(payload.Data.Resources.IPv6)

	if len(res.ASNs) == 0 {
		return nil, fmt.Errorf("%s 没有可用的已公告 ASN", country)
	}
	return res, nil
}

// normalizePrefixes 归一化并去重 CIDR 清单
func normalizePrefixes(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		_, network, err := net.ParseCIDR(strings.TrimSpace(item))
		if err != nil {
			continue
		}
		prefix := network.String()
		if _, ok := seen[prefix]; ok {
			continue
		}
		seen[prefix] = struct{}{}
		out = append(out, prefix)
	}
	return out
}

// CountryResourceTimeout 返回国家 ASN 清单查询的建议超时
func CountryResourceTimeout() time.Duration {
	return 20 * time.Second
}
