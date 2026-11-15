package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/oschwald/geoip2-golang"
)

// NormalizeDialFamily 归一化目标连接协议族配置，取值 ""（不限定）/ "ipv4" / "ipv6"
func NormalizeDialFamily(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ipv4", "4", "v4":
		return "ipv4"
	case "ipv6", "6", "v6":
		return "ipv6"
	default:
		return ""
	}
}

// FamilyDialContext 把拨号固定到指定协议族；family 为空时返回原 dial。
// 用于「仅 IPv4 / 仅 IPv6」的目标域名校验，保证校验走的就是该协议族。
func FamilyDialContext(
	dial func(ctx context.Context, network, addr string) (net.Conn, error),
	family string,
) func(ctx context.Context, network, addr string) (net.Conn, error) {
	normalized := NormalizeDialFamily(family)
	if normalized == "" || dial == nil {
		return dial
	}
	target := FamilyIPv4
	if normalized == "ipv6" {
		target = FamilyIPv6
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dial(ctx, familyDialNetwork(network, target), addr)
	}
}

// NewFamilyDialer 返回按协议族固定的拨号函数（用于检测器内部自建连接）
func NewFamilyDialer(family string, timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}
	return FamilyDialContext(dialer.DialContext, family)
}

// Family 出口 IP 协议族
type Family int

const (
	// FamilyIPv4 仅使用 IPv4 出口
	FamilyIPv4 Family = 4
	// FamilyIPv6 仅使用 IPv6 出口
	FamilyIPv6 Family = 6
)

// String 返回协议族的显示名
func (f Family) String() string {
	if f == FamilyIPv6 {
		return "IPv6"
	}
	return "IPv4"
}

// familyDialNetwork 把通用网络类型固定到指定协议族
func familyDialNetwork(base string, family Family) string {
	switch family {
	case FamilyIPv6:
		if base == "tcp" {
			return "tcp6"
		}
		if base == "udp" {
			return "udp6"
		}
	default:
		if base == "tcp" {
			return "tcp4"
		}
		if base == "udp" {
			return "udp4"
		}
	}
	return base
}

// GetEgressProvidersForFamily 返回指定协议族专用的出口探测源
func GetEgressProvidersForFamily(family Family) []EgressProvider {
	if family == FamilyIPv6 {
		return []EgressProvider{
			{Name: "api6.ipify.org", URL: "https://api6.ipify.org", Parse: familyParser(FamilyIPv6)},
			{Name: "ipv6.icanhazip.com", URL: "https://ipv6.icanhazip.com", Parse: familyParser(FamilyIPv6)},
		}
	}
	return []EgressProvider{
		{Name: "api4.ipify.org", URL: "https://api4.ipify.org", Parse: familyParser(FamilyIPv4)},
		{Name: "ipv4.icanhazip.com", URL: "https://ipv4.icanhazip.com", Parse: familyParser(FamilyIPv4)},
	}
}

// familyParser 解析纯文本 IP 响应，并校验协议族一致
func familyParser(family Family) func(body []byte) (*EgressInfo, error) {
	return func(body []byte) (*EgressInfo, error) {
		ip := strings.TrimSpace(string(body))
		parsed := net.ParseIP(ip)
		if parsed == nil {
			return nil, fmt.Errorf("响应不是有效 IP: %q", ip)
		}
		isV4 := parsed.To4() != nil
		if (family == FamilyIPv4) != isV4 {
			return nil, fmt.Errorf("响应 %s 与请求协议族 %s 不一致", ip, family)
		}
		return &EgressInfo{IP: ip, CountryCode: "UNKNOWN"}, nil
	}
}

// CheckEgressByFamily 仅通过指定协议族探测本机公网出口 IP 并补全归属地
func CheckEgressByFamily(ctx context.Context, family Family, reader *geoip2.Reader) (*EgressInfo, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, familyDialNetwork(network, family), addr)
		},
	}
	client := &http.Client{Transport: transport, Timeout: 6 * time.Second}

	var lastErr error
	for _, p := range GetEgressProvidersForFamily(family) {
		providerCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		req, err := http.NewRequestWithContext(providerCtx, http.MethodGet, p.URL, nil)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "curl/8.0.0 (RealityChecker)")

		resp, err := client.Do(req)
		if err != nil {
			cancel()
			lastErr = fmt.Errorf("请求失败: %w", err)
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()

		if readErr != nil {
			lastErr = fmt.Errorf("读取响应失败: %w", readErr)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("响应状态码 %d", resp.StatusCode)
			continue
		}
		info, err := p.Parse(body)
		if err != nil {
			lastErr = err
			continue
		}
		info.Source = p.Name
		enrichWithGeoIP(info, "", reader)
		return info, nil
	}

	if lastErr == nil {
		lastErr = errors.New("没有可用的探测源")
	}
	return nil, lastErr
}
