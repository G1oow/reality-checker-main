package cmd

import (
	"strings"

	"RealityChecker/internal/logger"
)

// executeBatch 执行批量检测
func (r *RootCmd) executeBatch(domainsStr string) {
	// 解析域名列表
	domains, invalidDomains, duplicateDomains := parseDomains(domainsStr)

	if len(domains) == 0 {
		logger.Error("有效目标缺失：没有可扫描的域名")
		logger.Detail("请确认域名格式，例如 apple.com、google.com")
		return
	}

	// 显示重复域名警告
	if len(duplicateDomains) > 0 {
		logger.Warn("重复域名：%d 个，已去重", len(duplicateDomains))

		// 只显示前5个重复域名，避免显示过多
		displayCount := 5
		if len(duplicateDomains) < displayCount {
			displayCount = len(duplicateDomains)
		}

		for i := 0; i < displayCount; i++ {
			logger.Detail("%s", duplicateDomains[i])
		}

		// 如果还有更多重复域名，显示省略提示
		if len(duplicateDomains) > displayCount {
			logger.Detail("...以及其余 %d 个重复域名", len(duplicateDomains)-displayCount)
		}

		logger.Blank()
	}

	// 显示无效域名警告
	if len(invalidDomains) > 0 {
		logger.Warn("无效域名：%d 个，已跳过", len(invalidDomains))

		// 只显示前5个无效域名，避免显示过多
		displayCount := 5
		if len(invalidDomains) < displayCount {
			displayCount = len(invalidDomains)
		}

		for i := 0; i < displayCount; i++ {
			logger.Detail("%s", invalidDomains[i])
		}

		// 如果还有更多无效域名，显示省略提示
		if len(invalidDomains) > displayCount {
			logger.Detail("...以及其余 %d 个无效域名", len(invalidDomains)-displayCount)
		}

		logger.Blank()
	}

	logger.Step("批量扫描：%d 个域名", len(domains))

	_, err := r.batchManager.CheckDomains(r.ctx, domains)
	if err != nil {
		logger.Error("批量扫描失败：%v", err)
		return
	}

	// 详细结果已在batch manager中打印，无需重复
}

// parseDomains 解析域名列表，返回有效域名、无效域名和重复域名
func parseDomains(domainsStr string) ([]string, []string, []string) {
	var validDomains []string
	var invalidDomains []string
	var duplicateDomains []string
	domainSet := make(map[string]bool)    // 用于去重
	duplicateSet := make(map[string]bool) // 用于记录重复域名

	// 支持空格分隔的域名列表
	fields := strings.Fields(domainsStr)
	for _, domain := range fields {
		domain = strings.TrimSpace(domain)
		if domain == "" {
			continue
		}

		if isValidDomain(domain) {
			// 检查是否已存在，避免重复
			if !domainSet[domain] {
				validDomains = append(validDomains, domain)
				domainSet[domain] = true
			} else {
				// 记录重复的有效域名
				if !duplicateSet[domain] {
					duplicateDomains = append(duplicateDomains, domain)
					duplicateSet[domain] = true
				}
			}
		} else {
			// 无效域名也去重
			if !domainSet[domain] {
				invalidDomains = append(invalidDomains, domain)
				domainSet[domain] = true
			} else {
				// 记录重复的无效域名
				if !duplicateSet[domain] {
					duplicateDomains = append(duplicateDomains, domain)
					duplicateSet[domain] = true
				}
			}
		}
	}
	return validDomains, invalidDomains, duplicateDomains
}
