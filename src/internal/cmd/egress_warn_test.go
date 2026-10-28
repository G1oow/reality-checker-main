package cmd

import "testing"

// 视角提示的判定矩阵：
// 出口 CN -> 任何目标都不提示（不存在视角失真）
// 出口非 CN + 目标 CN -> 提示
// 出口非 CN + 目标非 CN -> 不提示（海外出口扫海外段才是正常视角）
// 出口未知 -> 不提示
func TestWarnEgressMismatch(t *testing.T) {
	cases := []struct {
		name       string
		egress     *EgressInfo
		targetArg  string
		countryStr string
	}{
		{name: "出口CN_目标CN", egress: &EgressInfo{IP: "1.1.1.1", CountryCode: "CN", IsCN: true, Known: true}, countryStr: "CN"},
		{name: "出口SG_目标CN", egress: &EgressInfo{IP: "103.3.60.229", CountryCode: "SG", IsCN: false, Known: true}, countryStr: "CN"},
		{name: "出口SG_目标US", egress: &EgressInfo{IP: "103.3.60.229", CountryCode: "SG", IsCN: false, Known: true}, countryStr: "US"},
		{name: "出口SG_目标未知", egress: &EgressInfo{IP: "103.3.60.229", CountryCode: "SG", IsCN: false, Known: true}},
		{name: "出口未知", egress: &EgressInfo{Known: false}, countryStr: "CN"},
		{name: "未注入出口", egress: nil, countryStr: "CN"},
		{name: "出口SG_CIDR输入", egress: &EgressInfo{IP: "103.3.60.229", CountryCode: "SG", IsCN: false, Known: true}, targetArg: "203.0.113.0/24"},
		{name: "出口SG_非法IP输入", egress: &EgressInfo{IP: "103.3.60.229", CountryCode: "SG", IsCN: false, Known: true}, targetArg: "not-an-ip"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &RootCmd{egress: c.egress}
			// 只要不 panic、不阻塞即视为通过；提示内容走 logger，无法在单测中断言
			r.warnEgressMismatch(c.targetArg, "", c.countryStr)
		})
	}
}

// SetEgressInfo 应在 NewRootCmd 之前暂存出口信息，供 RootCmd 读取
func TestSetEgressInfoCarriesToRootCmd(t *testing.T) {
	prev := pendingEgress
	defer func() { pendingEgress = prev }()

	SetEgressInfo("103.3.60.229", "SG", false)
	if pendingEgress == nil {
		t.Fatal("expected pendingEgress to be set")
	}
	if !pendingEgress.Known || pendingEgress.CountryCode != "SG" || pendingEgress.IsCN {
		t.Fatalf("unexpected egress info: %+v", *pendingEgress)
	}

	SetEgressInfo("1.2.4.8", "CN", true)
	if !pendingEgress.Known || !pendingEgress.IsCN {
		t.Fatalf("expected CN egress to be known and IsCN, got %+v", *pendingEgress)
	}

	// 探测失败时 IP 为空，应标记为未知，避免误触发视角提示
	SetEgressInfo("", "", false)
	if pendingEgress.Known {
		t.Fatal("expected empty IP to be marked as unknown")
	}
}
