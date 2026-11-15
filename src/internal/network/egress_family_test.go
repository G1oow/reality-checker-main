package network

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// errDialStub 用于记录拨号参数而不产生真实连接
var errDialStub = errors.New("stub dial")

func TestNormalizeDialFamily(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"both", ""},
		{"unknown", ""},
		{"ipv4", "ipv4"},
		{"4", "ipv4"},
		{"v4", "ipv4"},
		{"V6", "ipv6"},
		{" ipv6 ", "ipv6"},
		{"6", "ipv6"},
	}
	for _, tc := range cases {
		if got := NormalizeDialFamily(tc.input); got != tc.want {
			t.Fatalf("NormalizeDialFamily(%q) = %q，期望 %q", tc.input, got, tc.want)
		}
	}
}

// FamilyDialContext 必须把通用网络类型固定到目标协议族：仅 IPv6 校验时域名必须走 tcp6。
func TestFamilyDialContextPinsProtocolFamily(t *testing.T) {
	var got []string
	stub := func(_ context.Context, network, addr string) (net.Conn, error) {
		got = append(got, network+" "+addr)
		return nil, errDialStub
	}

	calls := []struct {
		family  string
		network string
	}{
		{"", "tcp"},
		{"ipv4", "tcp"},
		{"ipv6", "tcp"},
		{"ipv6", "udp"},
	}
	for _, call := range calls {
		dial := FamilyDialContext(stub, call.family)
		if _, err := dial(context.Background(), call.network, "example.com:443"); !errors.Is(err, errDialStub) {
			t.Fatalf("协议族 %q 的拨号不应返回 %v", call.family, err)
		}
	}

	want := []string{
		"tcp example.com:443",
		"tcp4 example.com:443",
		"tcp6 example.com:443",
		"udp6 example.com:443",
	}
	if len(got) != len(want) {
		t.Fatalf("拨号次数不正确：%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 次拨号网络类型错误，实际 %q，期望 %q", i+1, got[i], want[i])
		}
	}

	if dial := FamilyDialContext(nil, "ipv6"); dial != nil {
		t.Fatalf("拨号函数为空时应原样返回 nil，而不是包装出 panic 风险")
	}
}

func TestNewFamilyDialerIsUsable(t *testing.T) {
	if dial := NewFamilyDialer("ipv6", time.Second); dial == nil {
		t.Fatalf("按协议族构造的拨号函数不应为空")
	}
	if dial := NewFamilyDialer("", time.Second); dial == nil {
		t.Fatalf("不限定协议族时也应返回可直接拨号的函数")
	}
}

func TestFamilyDialNetwork(t *testing.T) {
	cases := []struct {
		base   string
		family Family
		want   string
	}{
		{"tcp", FamilyIPv4, "tcp4"},
		{"udp", FamilyIPv4, "udp4"},
		{"tcp", FamilyIPv6, "tcp6"},
		{"udp", FamilyIPv6, "udp6"},
		{"tcp4", FamilyIPv6, "tcp4"},
	}
	for _, tc := range cases {
		if got := familyDialNetwork(tc.base, tc.family); got != tc.want {
			t.Fatalf("familyDialNetwork(%q, %v) = %q，期望 %q", tc.base, tc.family, got, tc.want)
		}
	}
}
