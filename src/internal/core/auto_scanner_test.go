package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"RealityChecker/internal/storage"
	"RealityChecker/internal/types"
)

func TestAutoScanner_EmptyCIDRs(t *testing.T) {
	scanner := NewAutoScanner(nil, nil, nil, AutoScanConfig{
		CIDRs: []string{},
	}, AutoScanEvents{})

	_, err := scanner.Run(context.Background())
	if !errors.Is(err, ErrNoCIDRsProvided) {
		t.Fatalf("expected ErrNoCIDRsProvided, got %v", err)
	}
}

func TestAutoScanner_CalculateRemainingIPs(t *testing.T) {
	cidrs := []string{
		"192.168.1.0/24",
		"10.0.0.0/16",
	}

	// 1. 无断点: 256 + 65536 = 65792
	total := CalculateRemainingIPs(cidrs, nil)
	if total != 65792 {
		t.Errorf("expected 65792, got %d", total)
	}

	// 2. 一个网段完成
	checkpoints := map[string]*storage.CheckpointRecord{
		"192.168.1.0/24": {
			CIDR:      "192.168.1.0/24",
			Completed: true,
		},
	}
	remaining := CalculateRemainingIPs(cidrs, checkpoints)
	if remaining != 65536 {
		t.Errorf("expected 65536, got %d", remaining)
	}
}

func TestAutoScanner_InitialResultsLoaded(t *testing.T) {
	initial := []*types.DetectionResult{
		{Domain: "example.com", Suitable: true},
		{Domain: "test.org", Suitable: true},
	}

	scanner := NewAutoScanner(nil, nil, nil, AutoScanConfig{
		CIDRs:          []string{"1.2.3.4"},
		InitialResults: initial,
	}, AutoScanEvents{})

	if len(scanner.suitableResults) != 2 {
		t.Errorf("expected 2 initial suitable results, got %d", len(scanner.suitableResults))
	}
	if !scanner.domainSet["example.com"] || !scanner.domainSet["test.org"] {
		t.Errorf("domainSet missing initial results")
	}
}

func TestAutoScanner_CandidateRejectedEvent(t *testing.T) {
	rejectedReported := false
	var reportedReason string

	events := AutoScanEvents{
		OnCandidateRejected: func(ip string, domain string, reason string) {
			rejectedReported = true
			reportedReason = reason
		},
	}

	filter := types.RealityFilterConfig{
		ExcludeSuffixes: []string{".top"},
	}

	scanner := NewAutoScanner(nil, nil, nil, AutoScanConfig{
		CIDRs:   []string{"127.0.0.1"},
		Filter:  filter,
		Verbose: true,
	}, events)

	// 模拟候选结果进入过滤阶段
	res := &types.DetectionResult{
		Domain:   "bad-node.top",
		Suitable: true,
	}

	passed, reason := FilterTarget(res, scanner.config.Filter)
	if passed {
		t.Fatalf("expected target with .top to be rejected")
	}

	if scanner.events.OnCandidateRejected != nil {
		scanner.events.OnCandidateRejected("1.2.3.4", res.Domain, reason)
	}

	if !rejectedReported {
		t.Errorf("expected OnCandidateRejected to be called")
	}
	if reportedReason == "" {
		t.Errorf("expected non-empty rejection reason")
	}
}

func TestCidrIsIPv6(t *testing.T) {
	cases := map[string]bool{
		"198.51.100.0/24": false,
		"10.0.0.0/8":      false,
		"198.51.100.7":    false,
		"2001:db8::/126":  true,
		"2001:db8::1":     true,
		" 2001:db8::1  ":  true,
		"":                false,
		"not-a-cidr":      false,
	}
	for cidr, want := range cases {
		if got := cidrIsIPv6(cidr); got != want {
			t.Fatalf("cidrIsIPv6(%q) = %v，期望 %v（IPv6 网段曾被硬编码开关静默跳过）", cidr, got, want)
		}
	}
}

func TestShuffleCIDRs(t *testing.T) {
	original := []string{
		"192.0.2.0/24",
		"198.51.100.0/24",
		"203.0.113.0/24",
		"192.0.2.128/25",
		"198.51.100.128/25",
		"203.0.113.128/25",
	}

	cidrs := append([]string(nil), original...)
	orders := make(map[string]bool)
	for i := 0; i < 50; i++ {
		shuffleCIDRs(cidrs)

		seen := make(map[string]int, len(cidrs))
		for _, c := range cidrs {
			seen[c]++
		}
		for _, c := range original {
			if seen[c] != 1 {
				t.Fatalf("随机化后网段 %q 出现 %d 次，应恰好 1 次", c, seen[c])
			}
		}
		orders[strings.Join(cidrs, ",")] = true
	}

	if len(orders) < 2 {
		t.Errorf("50 次随机化始终得到同一顺序，未实现网段级随机轮询")
	}

	// 单网段与空列表不应被改动
	single := []string{"192.0.2.0/24"}
	shuffleCIDRs(single)
	if len(single) != 1 || single[0] != "192.0.2.0/24" {
		t.Errorf("单网段被意外改动: %v", single)
	}
	shuffleCIDRs(nil)
}
