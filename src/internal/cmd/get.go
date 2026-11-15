package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"RealityChecker/internal/app"
	"RealityChecker/internal/config"
	"RealityChecker/internal/logger"
	"RealityChecker/internal/service"
	"RealityChecker/internal/version"
)

func (r *RootCmd) executeGet(args []string) {
	flags := flag.NewFlagSet("get", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		PrintCommandUsage("get <ip> [选项]", "以指定 IP 极速获取优质伪装目标（缓存优先 + 流式现扫）", flags)
	}

	limitFlag := flags.Int("limit", 5, "需要获取的伪装目标数量")
	flags.IntVar(limitFlag, "n", 5, "目标数量 (缩写)")

	minStarsFlag := flags.Int("stars", 3, "最低星级评分 (1-5)")
	flags.IntVar(minStarsFlag, "s", 3, "最低星级评分 (缩写)")

	freshFlag := flags.Bool("fresh", false, "强制重新扫描，不读取本地数据库缓存")
	jsonFlag := flags.Bool("json", false, "以格式化 JSON 方式输出结果")
	countryFlag := flags.String("country", "", "覆盖或指定所属国家 ISO 代码 (如 US, DE)")
	ipv4OnlyFlag := flags.Bool("4", false, "仅匹配 IPv4")
	ipv6OnlyFlag := flags.Bool("6", false, "仅匹配 IPv6")
	noCNFlag := flags.Bool("no-cn", true, "严格排除国内 .cn 域名")

	dbFlag := flags.String("db", "data/reality_targets.db", "SQLite 资产数据库文件路径")
	mmdbFlag := flags.String("mmdb", "data/Country.mmdb", "GeoIP 国家数据库文件路径")

	if err := flags.Parse(args); err != nil {
		return
	}

	if flags.NArg() < 1 {
		logger.Error("目标参数缺失：缺少目标 IP 地址")
		logger.Block(
			fmt.Sprintf("用法: %s get <ip> [--limit 5] [--stars 3] [--fresh] [--json]", version.GetAppName()),
			fmt.Sprintf("示例: %s get 203.0.113.10 -n 5 -s 4", version.GetAppName()),
		)
		return
	}

	targetIP := flags.Arg(0)

	cfg, _ := config.LoadConfig("")

	// 1. 初始化统一应用依赖容器
	container, err := app.NewContainer(app.ContainerOptions{
		Config:       cfg,
		ExplicitDB:   *dbFlag,
		ExplicitMMDB: *mmdbFlag,
	})
	if err != nil {
		logger.Error("容器初始化失败：%v", err)
		return
	}
	defer container.Close()

	if _, err := container.GetStore(); err != nil && !*jsonFlag {
		logger.Warn("资产库打开失败：%v（改用纯扫描模式）", err)
	}

	// 2. 获取端到端供给服务
	provService, err := container.GetProvisionService()
	if err != nil {
		logger.Error("供给服务构建失败：%v", err)
		return
	}

	req := service.ProvisionRequest{
		IP:          targetIP,
		Limit:       *limitFlag,
		MinStars:    *minStarsFlag,
		Fresh:       *freshFlag,
		Country:     *countryFlag,
		IPv4Only:    *ipv4OnlyFlag,
		IPv6Only:    *ipv6OnlyFlag,
		RequireNoCN: *noCNFlag,
	}

	if !*jsonFlag {
		logger.Blank()
		logger.Block(fmt.Sprintf("%s REALITY 优质目标极速供给引擎 [%s]", logger.IconStep, version.GetVersion()))
		logger.Linef(logger.IconStep, "入口目标", "%s（需匹配 %d 个 ≥%d 星目标）", targetIP, *limitFlag, *minStarsFlag)
	}

	var initData *service.InitEventData
	var targets []service.TargetItem
	var doneData *service.DoneEventData

	err = provService.StreamTargets(r.ctx, req, func(evt service.ProvisionEvent) error {
		switch evt.Event {
		case "init":
			if d, ok := evt.Data.(service.InitEventData); ok {
				initData = &d
				if !*jsonFlag {
					logger.Linef(logger.IconDetail, "归属信息", "ASN %s、国家 %s、网段 %d 个、本地缓存 %d 条", d.ASN, d.Country, d.CIDRCount, d.CachedHits)
					if d.CachedHits > 0 && !*freshFlag {
						logger.Detail("优先从本地已验证资产中提取")
					} else {
						logger.Detail("启动现场就近网段并发扫描")
					}
				}
			}
		case "progress":
			if p, ok := evt.Data.(service.ProgressEventData); ok && !*jsonFlag {
				logger.AboveBar(nil, "\r\033[K[扫描进度] 已扫描 IP: %d | 当前网段: %s | 当前 IP: %s",
					p.ScannedIPs, p.CurrentCIDR, p.CurrentIP)
			}
		case "target":
			if item, ok := evt.Data.(service.TargetItem); ok {
				targets = append(targets, item)
				if !*jsonFlag {
					sourceLabel := "本地缓存"
					if item.Source == "scan" {
						sourceLabel = "现场发现"
					}
					starStr := strings.Repeat("★", item.Stars)
					logger.AboveBar(nil, "\r\033[K%s 命中目标（%s）%s  %-30s  IP: %-15s  延迟: %4dms  证书: %3d天  页面: %d\n",
						logger.IconOK, sourceLabel, starStr, item.Domain, item.IP, item.HandshakeMS, item.CertDays, item.StatusCode)
				}
			}
		case "done":
			if d, ok := evt.Data.(service.DoneEventData); ok {
				doneData = &d
				if !*jsonFlag {
					logger.Blank()
					logger.Success("供给完成：共获取 %d 个目标（本地库 %d，现场扫描 %d，原因 %s）", d.TotalFound, d.FromCache, d.FromScan, d.Reason)
					logger.Blank()
				}
			}
		case "error":
			if !*jsonFlag {
				logger.Error("供给失败：%v", evt.Data)
			}
		}
		return nil
	})

	if *jsonFlag {
		output := map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"init":    initData,
				"targets": targets,
				"meta":    doneData,
			},
		}
		if err != nil {
			output["code"] = 500
			output["message"] = err.Error()
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(output)
	} else if err != nil {
		logger.Error("执行失败：%v", err)
	}
}
