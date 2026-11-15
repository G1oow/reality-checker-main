package detectors

import (
	"bufio"
	"fmt"
	"strings"

	"RealityChecker/internal/data"
	"RealityChecker/internal/logger"
	"RealityChecker/internal/types"
)

// BlockedStage 被墙检测阶段
type BlockedStage struct {
	gfwlist map[string]bool
}

// NewBlockedStage 创建被墙检测阶段
func NewBlockedStage() *BlockedStage {
	stage := &BlockedStage{
		gfwlist: make(map[string]bool),
	}
	stage.loadGFWList()
	return stage
}

// Execute 执行被墙检测
func (bs *BlockedStage) Execute(ctx *types.PipelineContext) error {
	// 如果配置关闭了 GFW 静态黑名单检测，直接跳过
	if ctx.Config != nil && !ctx.Config.GetFilter().CheckGFW {
		return nil
	}

	// 检查是否被墙
	isBlocked, reason := bs.checkBlocked(ctx.Domain)

	ctx.Result.Blocked = &types.BlockedResult{
		IsBlocked:      isBlocked,
		BlockedReasons: []string{reason},
		MatchType:      "gfwlist",
	}

	if isBlocked {
		ctx.EarlyExit = true
		return fmt.Errorf("域名被墙（%s）", reason)
	}
	return nil
}

// checkBlocked 检查是否被墙
func (bs *BlockedStage) checkBlocked(domain string) (bool, string) {
	domain = strings.ToLower(domain)

	// 检查GFWList
	if bs.gfwlist[domain] {
		return true, "仅参考黑名单"
	}

	// 检查通配符匹配
	parts := strings.Split(domain, ".")
	for i := 0; i < len(parts); i++ {
		wildcard := "*." + strings.Join(parts[i:], ".")
		if bs.gfwlist[wildcard] {
			return true, fmt.Sprintf("通配符匹配: %s", wildcard)
		}
	}

	return false, ""
}

// loadGFWList 加载 GFW 名单：优先读取外部 data/gfwlist.conf，缺失时回退内置资源 gfwlist.conf
func (bs *BlockedStage) loadGFWList() {
	reader, err := data.OpenRuleReader("data/gfwlist.conf", "gfwlist.conf")
	if err != nil {
		logger.Warn("GFW 名单不可用：%v，将跳过该检测", err)
		return
	}
	defer reader.Close()

	scanner := bufio.NewScanner(reader)
	inPayload := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if line == "payload:" {
			inPayload = true
			continue
		}

		// Clash 规则格式：payload 段内的 "  - '+.domain'"
		if inPayload {
			if strings.HasPrefix(line, "- '") && strings.HasSuffix(line, "'") {
				domain := strings.TrimPrefix(line, "- '")
				domain = strings.TrimSuffix(domain, "'")

				domain = strings.TrimPrefix(domain, "+.")

				if domain != "" {
					bs.gfwlist[domain] = true
				}
			}
			continue
		}

		// 兼容每行一个纯域名的格式
		bs.gfwlist[line] = true
	}
}

// CanEarlyExit 是否可以早期退出
func (bs *BlockedStage) CanEarlyExit() bool {
	return true
}

// Priority 优先级
func (bs *BlockedStage) Priority() int {
	return 1 // 被墙检测优先级最高
}

// Name 阶段名称
func (bs *BlockedStage) Name() string {
	return "blocked"
}
