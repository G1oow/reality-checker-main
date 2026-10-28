package asn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"RealityChecker/internal/network"
)

// CountryLookup 用于在本地 GeoIP 库缺少记录时（典型场景：IPv6 段覆盖不全）
// 通过在线接口反查 IP 的 ISO 3166-1 alpha-2 国家代码。
type CountryLookup struct {
	httpClient *http.Client
}

// NewCountryLookup 创建在线归属地查询器；proxyURL 为空表示直连。
func NewCountryLookup(proxyURL string, timeout ...time.Duration) *CountryLookup {
	requestTimeout := 8 * time.Second
	if len(timeout) > 0 && timeout[0] > 0 {
		requestTimeout = timeout[0]
	}
	httpClient, err := network.MakeHTTPClient(proxyURL, requestTimeout)
	if err != nil || httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}
	return &CountryLookup{httpClient: httpClient}
}

// countryProvider 描述一个在线归属地数据源
type countryProvider struct {
	name  string
	url   func(ip string) string
	parse func(body []byte) (string, error)
}

// normalizeCode 校验并规范化两位国家代码
func normalizeCode(raw string) (string, error) {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if len(code) != 2 {
		return "", fmt.Errorf("非法国家代码: %q", raw)
	}
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return "", fmt.Errorf("非法国家代码: %q", raw)
		}
	}
	return code, nil
}

// providers 按可靠性排序的数据源。IPv4/IPv6 均可用，且都无需 API Key。
func (c *CountryLookup) providers() []countryProvider {
	return []countryProvider{
		{
			name: "ipinfo.io",
			url:  func(ip string) string { return "https://ipinfo.io/" + ip + "/json" },
			parse: func(body []byte) (string, error) {
				var res struct {
					Country string `json:"country"`
					Bogon   bool   `json:"bogon"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					return "", err
				}
				if res.Bogon {
					return "", fmt.Errorf("地址属于保留/私有网段")
				}
				return res.Country, nil
			},
		},
		{
			name: "api.ip.sb",
			url:  func(ip string) string { return "https://api.ip.sb/geoip/" + ip },
			parse: func(body []byte) (string, error) {
				var res struct {
					CountryCode string `json:"country_code"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					return "", err
				}
				return res.CountryCode, nil
			},
		},
		{
			name: "ip-api.com",
			url: func(ip string) string {
				return "http://ip-api.com/json/" + ip + "?fields=status,countryCode,message"
			},
			parse: func(body []byte) (string, error) {
				var res struct {
					Status      string `json:"status"`
					CountryCode string `json:"countryCode"`
					Message     string `json:"message"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					return "", err
				}
				if !strings.EqualFold(res.Status, "success") {
					return "", fmt.Errorf("%s", res.Message)
				}
				return res.CountryCode, nil
			},
		},
	}
}

// LookupCountry 逐个尝试在线数据源，返回首个成功解析的国家代码。
// IPv6 地址同样支持；全部数据源失败时返回错误并附带各源失败原因。
func (c *CountryLookup) LookupCountry(ctx context.Context, ip string) (string, error) {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return "", fmt.Errorf("无效 IP: %s", ip)
	}
	// 私有/回环地址没有公网归属，直接判失败，避免无谓请求
	if parsed.IsPrivate() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast() {
		return "", fmt.Errorf("IP %s 属于私有或保留网段，无公网归属", ip)
	}

	var failures []string
	for _, p := range c.providers() {
		reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, p.url(ip), nil)
		if err != nil {
			cancel()
			failures = append(failures, fmt.Sprintf("%s: %v", p.name, err))
			continue
		}
		req.Header.Set("User-Agent", "curl/8.0.0 (RealityChecker)")
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			cancel()
			failures = append(failures, fmt.Sprintf("%s: %v", p.name, err))
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		cancel()

		if readErr != nil {
			failures = append(failures, fmt.Sprintf("%s: 读取响应失败: %v", p.name, readErr))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			failures = append(failures, fmt.Sprintf("%s: 状态码 %d", p.name, resp.StatusCode))
			continue
		}

		raw, err := p.parse(body)
		if err != nil || strings.TrimSpace(raw) == "" {
			failures = append(failures, fmt.Sprintf("%s: 无法解析国家", p.name))
			continue
		}
		code, err := normalizeCode(raw)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", p.name, err))
			continue
		}
		return code, nil
	}

	return "", fmt.Errorf("所有在线归属地数据源均失败: %s", strings.Join(failures, "; "))
}
