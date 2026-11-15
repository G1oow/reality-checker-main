package detectors

import (
	"net"
	"strings"
	"testing"

	"RealityChecker/internal/types"
)

// dialAddress 必须产出 net.Dial 能直接消费的 host:port。
// 回归背景：曾用 ip+":443" 直接拼接，IPv6 字面量会拼成 "2001:db8::1:443"，
// net.Dial 立刻返回 "too many colons in address"，导致只有 AAAA 记录的域名
// 被误判为「网络不可达」。
func TestDialAddress_IPv6NeedsBrackets(t *testing.T) {
	const v6 = "2001:db8::6440:1007"

	got := dialAddress(v6, "443")
	if want := "[" + v6 + "]:443"; got != want {
		t.Fatalf("dialAddress(%q, \"443\") = %q, want %q", v6, got, want)
	}

	// 用 net.SplitHostPort 校验：拼接错误的地址这里就会报错，
	// 且 Dial 依赖同一套解析，能真正守住回归。
	host, port, err := net.SplitHostPort(got)
	if err != nil {
		t.Fatalf("dialAddress produced unparseable address %q: %v", got, err)
	}
	if host != v6 {
		t.Fatalf("host = %q, want %q", host, v6)
	}
	if port != "443" {
		t.Fatalf("port = %q, want %q", port, "443")
	}
}

// 空方括号包裹的 IPv6 会被 Dial 接受，这里顺带覆盖 80 端口分支。
func TestDialAddress_IPv6Port80(t *testing.T) {
	got := dialAddress("2001:db8::1", "80")
	if want := "[2001:db8::1]:80"; got != want {
		t.Fatalf("dialAddress = %q, want %q", got, want)
	}
}

// IPv4 不得被加上方括号，否则 Dial 同样失败。
func TestDialAddress_IPv4Unbracketed(t *testing.T) {
	for _, tc := range []struct{ ip, port, want string }{
		{"192.0.2.10", "443", "192.0.2.10:443"},
		{"198.51.100.7", "80", "198.51.100.7:80"},
	} {
		if got := dialAddress(tc.ip, tc.port); got != tc.want {
			t.Fatalf("dialAddress(%q, %q) = %q, want %q", tc.ip, tc.port, got, tc.want)
		}
	}
}

// 仅 IPv6 校验时域名必须有 AAAA 记录，否则必须给出可读原因而不是静默跳过。
func TestSelectAddressByDialFamily(t *testing.T) {
	v4 := net.IPAddr{IP: net.ParseIP("198.51.100.7")}
	v6 := net.IPAddr{IP: net.ParseIP("2001:db8::7")}

	cases := []struct {
		name   string
		ips    []net.IPAddr
		family string
		want   string
		reason string
	}{
		{"仅 IPv4 且存在 A 记录", []net.IPAddr{v4, v6}, "ipv4", "198.51.100.7", ""},
		{"仅 IPv6 且存在 AAAA 记录", []net.IPAddr{v4, v6}, "ipv6", "2001:db8::7", ""},
		{"不限定协议族时优先 IPv4", []net.IPAddr{v4, v6}, "", "198.51.100.7", ""},
		{"不限定协议族且只有 IPv6", []net.IPAddr{{IP: nil}, v6}, "", "2001:db8::7", ""},
		{"仅 IPv4 但只有 AAAA", []net.IPAddr{v6}, "ipv4", "", "无 A 记录"},
		{"仅 IPv6 但只有 A 记录", []net.IPAddr{v4}, "ipv6", "", "无 AAAA 记录"},
		{"仅 IPv6 且无任何记录", nil, "ipv6", "", "无 AAAA 记录"},
		{"不限定协议族且无任何记录", nil, "", "", "未找到IP地址"},
	}
	for _, tc := range cases {
		got, err := selectAddress(tc.ips, tc.family)
		if tc.reason == "" {
			if err != nil {
				t.Fatalf("%s：不应报错，实际 %v", tc.name, err)
			}
			if got != tc.want {
				t.Fatalf("%s：应选中 %s，实际 %s", tc.name, tc.want, got)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s：应返回错误（%s），实际选中 %s", tc.name, tc.reason, got)
		}
		if !strings.Contains(err.Error(), tc.reason) {
			t.Fatalf("%s：错误信息应包含 %q，实际 %q", tc.name, tc.reason, err.Error())
		}
	}
}

// dialFamilyOf 从流水线配置中取出目标连接协议族。
func TestDialFamilyOf(t *testing.T) {
	if got := dialFamilyOf(nil); got != "" {
		t.Fatalf("空上下文应返回不限定协议族，实际 %q", got)
	}
	ctx := &types.PipelineContext{Config: &types.Config{}}
	if got := dialFamilyOf(ctx); got != "" {
		t.Fatalf("未配置协议族时应返回空串，实际 %q", got)
	}
	ctx.Config.Network.Family = "V6"
	if got := dialFamilyOf(ctx); got != "ipv6" {
		t.Fatalf("协议族应被归一化，实际 %q", got)
	}
}
