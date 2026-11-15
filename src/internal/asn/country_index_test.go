package asn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

// newCountryFakeClient 构造指向本地假 RIPE 接口的客户端，IPv6 固定返回 2001:db8::/32。
func newCountryFakeClient(t *testing.T, ipv4 map[string][]string, status int) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		country := r.URL.Query().Get("resource")
		payload := map[string]any{
			"data_call_status": "supported",
			"data": map[string]any{
				"query_time": "2026-01-01T00:00:00",
				"resources": map[string]any{
					"asn":  []string{"64500"},
					"ipv4": ipv4[country],
					"ipv6": []string{"2001:db8::/32"},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(server.Close)

	client := NewClient(5 * time.Second)
	client.SetCountryEndpoint(server.URL)
	return client
}

func TestCountryPrefixSetContains(t *testing.T) {
	set := newCountryPrefixSet([]string{
		"10.0.0.0/8",
		"192.0.2.0/24",
		"2001:db8::/32",
		"192.0.2.0/25", // 嵌套前缀不应破坏判定
	})

	cases := map[string]bool{
		"10.0.0.1":        true,
		"10.255.255.255":  true,
		"11.0.0.1":        false,
		"192.0.2.128":     true,
		"192.0.2.255":     true,
		"192.0.3.1":       false,
		"2001:db8::1":     true,
		"2001:db8:ffff::": true,
		"2001:db9::1":     false,
	}
	for raw, want := range cases {
		addr := netip.MustParseAddr(raw)
		if got := set.contains(addr); got != want {
			t.Fatalf("contains(%s) = %v，期望 %v", raw, got, want)
		}
	}
}

func TestCountryPrefixIndexMatchesFromRIPE(t *testing.T) {
	client := newCountryFakeClient(t, map[string][]string{
		"DE": {"198.51.100.0/24"},
	}, http.StatusOK)
	index := NewCountryPrefixIndex(client)
	ctx := context.Background()

	matched, err := index.Match(ctx, "DE", netip.MustParseAddr("198.51.100.7"))
	if err != nil || !matched {
		t.Fatalf("198.51.100.7 应属于 DE（err=%v matched=%v）", err, matched)
	}
	matched, err = index.Match(ctx, "de", netip.MustParseAddr("203.0.113.7"))
	if err != nil || matched {
		t.Fatalf("203.0.113.7 不应属于 DE（err=%v matched=%v）", err, matched)
	}
	matched, err = index.Match(ctx, "DE", netip.MustParseAddr("2001:db8::1"))
	if err != nil || !matched {
		t.Fatalf("2001:db8::1 应属于 DE（IPv6 前缀，err=%v matched=%v）", err, matched)
	}
	if _, err := index.Match(ctx, "DEU", netip.MustParseAddr("198.51.100.7")); err == nil {
		t.Fatalf("非法国家代码应报错")
	}
}

func TestCountryResolverFilterPrefixesUsesRIPE(t *testing.T) {
	client := newCountryFakeClient(t, map[string][]string{
		"DE": {"198.51.100.0/24"},
	}, http.StatusOK)
	resolver := NewCountryResolver(client, nil)

	kept, source := resolver.FilterPrefixes(context.Background(), []string{
		"198.51.100.0/24",
		"203.0.113.0/24",
	}, "DE")
	if source != CountrySourceRIPE {
		t.Fatalf("应使用 RIPE 数据源，实际 %q", source)
	}
	if len(kept) != 1 || kept[0] != "198.51.100.0/24" {
		t.Fatalf("应只保留属于 DE 的网段，实际 %v", kept)
	}
}

func TestCountryResolverKeepsPrefixesWhenSourcesUnavailable(t *testing.T) {
	client := newCountryFakeClient(t, nil, http.StatusInternalServerError)
	resolver := NewCountryResolver(client, nil)

	prefixes := []string{"198.51.100.0/24", "203.0.113.0/24"}
	kept, source := resolver.FilterPrefixes(context.Background(), prefixes, "DE")
	if source != CountrySourceNone {
		t.Fatalf("RIPE 不可用且无本地库时应返回 none，实际 %q", source)
	}
	if len(kept) != len(prefixes) {
		t.Fatalf("数据源不可用时应保守保留全部网段，实际 %v", kept)
	}
}

func TestCountryResolverWithoutSourcesReportsNone(t *testing.T) {
	resolver := NewCountryResolver(nil, nil)
	matched, source := resolver.MatchAddr(context.Background(), "DE", netip.MustParseAddr("198.51.100.7"))
	if matched || source != CountrySourceNone {
		t.Fatalf("无数据源时应返回 (false, none)，实际 (%v, %q)", matched, source)
	}
}
