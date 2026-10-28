package asn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 国家代码规范化：大小写、空白、非法值
func TestNormalizeCode(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"sg", "SG", false},
		{" SG ", "SG", false},
		{"CN", "CN", false},
		{"", "", true},
		{"S", "", true},
		{"SGP", "", true},
		{"S1", "", true},
	}
	for _, c := range cases {
		got, err := normalizeCode(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("normalizeCode(%q) 期望报错，实际得到 %q", c.in, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("normalizeCode(%q) = %q, %v; 期望 %q", c.in, got, err, c.want)
		}
	}
}

// 各数据源解析器：正常与异常返回
func TestCountryProviderParsers(t *testing.T) {
	p := (&CountryLookup{}).providers()
	if len(p) < 2 {
		t.Fatalf("期望至少 2 个数据源，实际 %d", len(p))
	}

	// ipinfo.io
	if code, err := p[0].parse([]byte(`{"country":"SG","bogon":false}`)); err != nil || code != "SG" {
		t.Errorf("ipinfo 正常响应解析失败: %q %v", code, err)
	}
	if _, err := p[0].parse([]byte(`{"bogon":true}`)); err == nil {
		t.Error("ipinfo bogon 响应应报错")
	}
	if _, err := p[0].parse([]byte(`not-json`)); err == nil {
		t.Error("ipinfo 非法 JSON 应报错")
	}

	// api.ip.sb
	if code, err := p[1].parse([]byte(`{"country_code":"JP"}`)); err != nil || code != "JP" {
		t.Errorf("ip.sb 正常响应解析失败: %q %v", code, err)
	}

	// ip-api.com
	if code, err := p[2].parse([]byte(`{"status":"success","countryCode":"US"}`)); err != nil || code != "US" {
		t.Errorf("ip-api 正常响应解析失败: %q %v", code, err)
	}
	if _, err := p[2].parse([]byte(`{"status":"fail","message":"invalid query"}`)); err == nil {
		t.Error("ip-api fail 响应应报错")
	}
}

// 私有/保留地址不应发起网络请求
func TestLookupCountryRejectsNonPublicIP(t *testing.T) {
	lookup := NewCountryLookup("")
	for _, ip := range []string{"10.0.0.1", "192.168.1.1", "127.0.0.1", "::1", "fe80::1"} {
		if _, err := lookup.LookupCountry(context.Background(), ip); err == nil {
			t.Errorf("%s 应该被拒绝，但返回了成功", ip)
		}
	}
	// 非法输入
	if _, err := lookup.LookupCountry(context.Background(), "not-an-ip"); err == nil {
		t.Error("非法 IP 应报错")
	}
}

// 首个数据源失败时应自动回退到下一个，并用 IPv6 地址验证路径正确
func TestLookupCountryFallsBackAcrossProviders(t *testing.T) {
	// 用 httptest 替换传输层：按 host 分别返回失败/成功
	lookup := NewCountryLookup("")
	lookup.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		switch {
		case req.URL.Host == "ipinfo.io":
			// 模拟限流
			rec.WriteHeader(http.StatusTooManyRequests)
		case req.URL.Host == "api.ip.sb":
			_ = json.NewEncoder(rec).Encode(map[string]string{"country_code": "SG"})
		default:
			rec.WriteHeader(http.StatusNotFound)
		}
		return rec.Result(), nil
	})

	code, err := lookup.LookupCountry(context.Background(), "2001:db8:beef::23f")
	if err != nil {
		t.Fatalf("期望回退成功，实际报错: %v", err)
	}
	if code != "SG" {
		t.Fatalf("期望 SG，实际 %q", code)
	}
}

// 全部数据源失败时，错误信息应包含各源原因
func TestLookupCountryAllProvidersFail(t *testing.T) {
	lookup := NewCountryLookup("")
	lookup.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusInternalServerError)
		return rec.Result(), nil
	})

	_, err := lookup.LookupCountry(context.Background(), "2001:db8:beef::23f")
	if err == nil {
		t.Fatal("全部失败时应报错")
	}
	msg := err.Error()
	for _, want := range []string{"ipinfo.io", "api.ip.sb", "ip-api.com"} {
		if !strings.Contains(msg, want) {
			t.Errorf("错误信息应包含数据源 %q，实际: %s", want, msg)
		}
	}
}

// roundTripFunc 便于在测试中替换 http.Transport
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

