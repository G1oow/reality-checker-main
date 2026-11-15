package panel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"RealityChecker/internal/asn"
	"RealityChecker/internal/core"
	"RealityChecker/internal/scanner"
	"RealityChecker/internal/types"
	"RealityChecker/internal/version"

	"github.com/oschwald/geoip2-golang"
	"golang.org/x/term"
)

// 交互式数字菜单面板：每轮重绘「横幅 + 状态 + 分组编号菜单」，输入编号进入对应
// 参数的子菜单，输入 s 直接扫描。不使用备用屏、不依赖方向键与焦点切换，因此 SSH、
// 手机终端、tmux、Windows Terminal 下的操作方式完全一致。

// Deps 面板运行时依赖
type Deps struct {
	Reader   *geoip2.Reader
	Resolver asn.ASNResolver
	Country  CountryClient
	Engine   *core.Engine
	Scanner  *scanner.Scanner
	BaseCfg  types.Config
	Probe    EntryProbeFunc
	Now      func() time.Time
}

// 面板用的 ANSI 颜色；非终端输出时不写入（避免管道里出现控制字符）
const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
)

const (
	// menuBoxWidth 横幅内宽（显示列）
	menuBoxWidth = 66
	// menuLabelWidth 菜单项名称的显示宽度
	menuLabelWidth = 14
	// menuKeyWidth 快捷键标签的显示宽度（容纳 [10] 这类两位编号）
	menuKeyWidth = 6
)

// MenuConfig 菜单依赖与输入输出（In/Out 可注入，便于用脚本化输入做测试）
type MenuConfig struct {
	Deps         Deps
	Settings     Settings
	SettingsPath string
	In           io.Reader
	Out          io.Writer
	// Color 为 nil 时按 Out 是否为终端自动判断；显式赋值便于测试固定行为
	Color *bool
	// Updater 为 nil 时按当前安装环境构造；测试可注入固定下载源
	Updater *Updater
	// Uninstall 为 nil 时按当前运行环境推导；测试可注入临时路径
	Uninstall *UninstallConfig
}

// Menu 交互式数字菜单面板
type Menu struct {
	ctx          context.Context
	deps         Deps
	settings     Settings
	settingsPath string

	in    *bufio.Reader
	out   io.Writer
	tty   bool
	color bool

	entries   []EntryOption
	probing   bool
	updater   *Updater
	uninstall UninstallConfig

	rows   []Row
	status Status

	noticeKind string
	notice     string

	quit bool
}

// NewMenu 创建交互式数字菜单面板
func NewMenu(ctx context.Context, cfg MenuConfig) *Menu {
	if ctx == nil {
		ctx = context.Background()
	}
	deps := cfg.Deps
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Probe == nil {
		deps.Probe = ProbeEntries
	}
	if strings.TrimSpace(cfg.SettingsPath) == "" {
		cfg.SettingsPath = DefaultSettingsPath()
	}
	settings := cfg.Settings
	settings.Normalize()

	in := cfg.In
	if in == nil {
		in = os.Stdin
	}
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}

	tty := false
	if file, ok := out.(*os.File); ok {
		tty = term.IsTerminal(int(file.Fd()))
	}
	color := tty
	if cfg.Color != nil {
		color = *cfg.Color
	}
	if os.Getenv("NO_COLOR") != "" {
		color = false
	}

	updater := cfg.Updater
	if updater == nil {
		updater = NewUpdater()
	}
	uninstall := DefaultUninstallConfig(cfg.SettingsPath)
	if cfg.Uninstall != nil {
		uninstall = *cfg.Uninstall
		if strings.TrimSpace(uninstall.SettingsPath) == "" {
			uninstall.SettingsPath = cfg.SettingsPath
		}
	}

	return &Menu{
		ctx:          ctx,
		deps:         deps,
		settings:     settings,
		settingsPath: cfg.SettingsPath,
		in:           bufio.NewReader(in),
		out:          out,
		tty:          tty,
		color:        color,
		status:       Status{Phase: PhaseIdle, Message: "就绪"},
		updater:      updater,
		uninstall:    uninstall,
	}
}

// Run 进入菜单主循环，直到用户选择退出
func (m *Menu) Run() error {
	m.probeEntries()
	for !m.quit && m.ctx.Err() == nil {
		m.render()
		line, ok := m.readLine(m.promptText())
		if !ok {
			// 标准输入结束（管道 / EOF）：按退出处理，避免空转
			m.quit = true
			break
		}
		m.handle(strings.ToLower(strings.TrimSpace(line)))
	}
	fmt.Fprint(m.out, "  已退出面板。\n")
	return nil
}

// Settings 返回当前面板参数
func (m *Menu) Settings() Settings { return m.settings }

// Rows 返回本次会话命中的目标
func (m *Menu) Rows() []Row { return m.rows }

// Status 返回最近一次扫描状态
func (m *Menu) Status() Status { return m.status }

// handle 分派一次用户输入
func (m *Menu) handle(choice string) {
	switch choice {
	case "":
		return
	case "0", "q", "quit", "exit":
		m.quit = true
	case "s":
		m.scan()
	case "d":
		m.showResults()
	case "r":
		m.probeEntries()
	case "u":
		m.checkUpdate()
	case "x":
		m.uninstallPanel()
	default:
		item, ok := m.item(choice)
		if !ok {
			m.noticef("warn", fmt.Sprintf("无效输入「%s」：请输入 1~%d 的编号，或 s 开始扫描 / d 查看结果 / r 重探出口 / u 检查更新 / x 卸载 / 0 退出", choice, m.itemCount()))
			return
		}
		item.Edit(m)
	}
}

// menuItem 一个可编辑的菜单项
type menuItem struct {
	Key   string
	Label string
	Value func() string
	Edit  func(*Menu)
}

// menuGroup 一组菜单项
type menuGroup struct {
	Title string
	Items []menuItem
}

// groups 返回全部参数分组（含当前取值）
func (m *Menu) groups() []menuGroup {
	return []menuGroup{
		{Title: "扫描参数", Items: []menuItem{
			{Key: "1", Label: "扫描模式", Value: m.modeValue, Edit: (*Menu).editMode},
			{Key: "2", Label: "校验协议族", Value: m.familyValue, Edit: (*Menu).editFamily},
			{Key: "3", Label: "国家/地区代码", Value: m.countryValue, Edit: (*Menu).editCountry},
			{Key: "4", Label: "目标数量", Value: m.limitValue, Edit: (*Menu).editLimit},
			{Key: "5", Label: "扫描并发", Value: m.threadsValue, Edit: (*Menu).editThreads},
			{Key: "6", Label: "单 IP 超时", Value: m.timeoutValue, Edit: (*Menu).editTimeout},
		}},
		{Title: "过滤策略", Items: []menuItem{
			{Key: "7", Label: "最低星级", Value: m.minStarsValue, Edit: (*Menu).editMinStars},
			{Key: "8", Label: "握手延迟上限", Value: m.handshakeValue, Edit: (*Menu).editHandshake},
			{Key: "9", Label: "证书剩余天数", Value: m.certDaysValue, Edit: (*Menu).editCertDays},
			{Key: "10", Label: "允许 CDN", Value: m.allowCDNValue, Edit: (*Menu).editAllowCDN},
			{Key: "11", Label: "允许热门大站", Value: m.allowHotValue, Edit: (*Menu).editAllowHot},
			{Key: "12", Label: "排除国内站点", Value: m.excludeCNValue, Edit: (*Menu).editExcludeCN},
			{Key: "13", Label: "排除默认页", Value: m.excludeDefaultValue, Edit: (*Menu).editExcludeDefault},
		}},
	}
}

// item 按编号查找菜单项
func (m *Menu) item(key string) (menuItem, bool) {
	for _, group := range m.groups() {
		for _, item := range group.Items {
			if item.Key == key {
				return item, true
			}
		}
	}
	return menuItem{}, false
}

// itemCount 返回编号菜单项总数
func (m *Menu) itemCount() int {
	total := 0
	for _, group := range m.groups() {
		total += len(group.Items)
	}
	return total
}

// ---- 渲染 ----

// render 重绘整屏菜单
func (m *Menu) render() {
	m.clearScreen()
	var builder strings.Builder
	builder.WriteString(m.renderBanner())
	builder.WriteString(m.renderStatus())
	for _, group := range m.groups() {
		builder.WriteString(m.renderGroup(group))
	}
	builder.WriteString(m.renderActions())
	builder.WriteString(m.renderMaintenance())
	builder.WriteString(m.renderNotice())
	fmt.Fprint(m.out, builder.String())
}

// renderBanner 渲染标题横幅
func (m *Menu) renderBanner() string {
	title := fmt.Sprintf("R-C-M · Reality Checker %s", version.GetVersion())
	subtitle := "伪装目标扫描面板 · 输入编号操作"
	border := func(left, right string) string {
		return m.cyan("  " + left + strings.Repeat("═", menuBoxWidth) + right)
	}
	line := func(text string) string {
		return m.cyan("  ║") + pad("  "+text, menuBoxWidth) + m.cyan("║")
	}
	return strings.Join([]string{
		"",
		border("╔", "╗"),
		line(title),
		line(subtitle),
		border("╚", "╝"),
	}, "\n") + "\n"
}

// renderStatus 渲染本机出口与运行状态
func (m *Menu) renderStatus() string {
	var builder strings.Builder
	builder.WriteString("\n")
	builder.WriteString(m.statusLine("本机出口", m.entriesSummary()))
	builder.WriteString(m.statusLine("发现来源", m.discoverySummary()))
	builder.WriteString(m.statusLine("运行状态", m.runStatusSummary()))
	return builder.String()
}

// statusLine 渲染一行「标签 + 取值」
func (m *Menu) statusLine(label, value string) string {
	return "  " + m.cyan(pad(label, 10)) + " " + value + "\n"
}

// renderGroup 渲染一组带编号的菜单项
func (m *Menu) renderGroup(group menuGroup) string {
	var builder strings.Builder
	builder.WriteString("\n  " + m.cyan("【"+group.Title+"】") + "\n")
	for _, item := range group.Items {
		builder.WriteString(fmt.Sprintf("    %s %s %s\n",
			m.green(pad("["+item.Key+"]", menuKeyWidth)),
			pad(item.Label, menuLabelWidth),
			item.Value()))
	}
	return builder.String()
}

// renderActions 渲染执行区
func (m *Menu) renderActions() string {
	var builder strings.Builder
	builder.WriteString("\n  " + m.cyan("【执行】") + "\n")
	builder.WriteString(fmt.Sprintf("    %s %s %s %s %s %s\n",
		m.green(pad("[s]", menuKeyWidth)), pad("开始扫描", menuLabelWidth),
		m.green(pad("[d]", menuKeyWidth)), pad("查看上次结果", menuLabelWidth),
		m.green(pad("[r]", menuKeyWidth)), "重新探测出口"))
	return builder.String()
}

// renderMaintenance 渲染维护区与退出项
func (m *Menu) renderMaintenance() string {
	var builder strings.Builder
	builder.WriteString("\n  " + m.cyan("【维护】") + "\n")
	builder.WriteString(fmt.Sprintf("    %s %s %s\n",
		m.green(pad("[u]", menuKeyWidth)), pad("检查更新", menuLabelWidth), "从 GitHub Releases 升级主程序"))
	builder.WriteString(fmt.Sprintf("    %s %s %s\n",
		m.green(pad("[x]", menuKeyWidth)), pad("卸载程序", menuLabelWidth), "删除程序与快捷指令（需二次确认）"))
	builder.WriteString("\n  " + m.dim(strings.Repeat("─", menuBoxWidth)) + "\n")
	builder.WriteString(fmt.Sprintf("    %s %s\n", m.red(pad("[0]", menuKeyWidth)), "退出面板（扫描参数已自动保存）"))
	return builder.String()
}

// renderNotice 渲染上一步操作的提示
func (m *Menu) renderNotice() string {
	if strings.TrimSpace(m.notice) == "" {
		return ""
	}
	return "\n  " + m.noticeStyle() + "\n"
}

// promptText 返回主菜单输入提示
func (m *Menu) promptText() string {
	return "\n  " + m.yellow(fmt.Sprintf("请输入选项 [1-%d / s / d / r / u / x / 0]: ", m.itemCount()))
}

// ---- 当前取值 ----

func (m *Menu) modeValue() string {
	switch m.settings.DiscoveryKind() {
	case DiscoveryCountryASN:
		return "按国家/地区 ASN"
	case DiscoveryVPSCountryASN:
		return m.dim("按 VPS 所在国家 ASN（仅 IPv6 校验固定）")
	}
	return "按 VPS 入口 IP ASN"
}

func (m *Menu) familyValue() string {
	value := familyLabel(m.settings.Family)
	if m.settings.VerifyByIPv6Only() {
		return value + m.dim("（发现改用 VPS 所在国家 ASN）")
	}
	return value
}

func (m *Menu) countryValue() string {
	if m.settings.Country != "" {
		if m.settings.CountryRowActive() {
			return m.settings.Country
		}
		return m.settings.Country + m.dim("（当前模式不使用）")
	}
	switch m.settings.DiscoveryKind() {
	case DiscoveryCountryASN:
		return m.yellow("未设置（必填，如 DE / JP）")
	case DiscoveryVPSCountryASN:
		if auto := m.autoCountry(); auto != "" {
			return "自动 " + auto + m.dim("（VPS 所在国家）")
		}
		return m.yellow("自动判定失败（可手动填写）")
	}
	return m.dim("—（仅国家模式 / 仅 IPv6 校验使用）")
}

func (m *Menu) limitValue() string {
	return fmt.Sprintf("%d", m.settings.Limit)
}

func (m *Menu) threadsValue() string {
	return fmt.Sprintf("%d", m.settings.Threads)
}

func (m *Menu) timeoutValue() string {
	return fmt.Sprintf("%ds", m.settings.TimeoutSec)
}

func (m *Menu) minStarsValue() string {
	return optionalCount(m.settings.Filter.MinStars, " 星")
}

func (m *Menu) handshakeValue() string {
	return optionalMS(m.settings.Filter.MaxHandshakeMS)
}

func (m *Menu) certDaysValue() string {
	return optionalCount(m.settings.Filter.MinCertDays, " 天")
}

func (m *Menu) allowCDNValue() string       { return yesNo(m.settings.Filter.AllowCDN) }
func (m *Menu) allowHotValue() string       { return yesNo(m.settings.Filter.AllowHot) }
func (m *Menu) excludeCNValue() string      { return yesNo(m.settings.Filter.ExcludeCN) }
func (m *Menu) excludeDefaultValue() string { return yesNo(m.settings.Filter.ExcludeDefaultPage) }

// entriesSummary 返回本机出口探测结果摘要
func (m *Menu) entriesSummary() string {
	if m.settings.DiscoveryKind() == DiscoveryCountryASN {
		return m.dim("当前为按国家/地区 ASN 模式，不使用本机出口")
	}
	if m.probing {
		return m.dim("正在探测本机出口 IPv4/IPv6…（最长 45 秒）")
	}
	if len(m.entries) == 0 {
		return m.yellow("尚未探测到出口 IP，按 r 重新探测")
	}
	parts := make([]string, 0, len(m.entries))
	for _, option := range m.entries {
		if option.Available {
			parts = append(parts, m.green("✔ ")+option.Summary())
			continue
		}
		parts = append(parts, m.yellow("! ")+option.Summary())
	}
	return strings.Join(parts, m.dim("  |  "))
}

// discoverySummary 返回发现来源说明
func (m *Menu) discoverySummary() string {
	switch m.settings.DiscoveryKind() {
	case DiscoveryCountryASN:
		return fmt.Sprintf("按国家/地区 ASN（%s，发现用 IPv4 网段，域名按 %s 校验）",
			emptyAs(m.settings.Country, "国家未填"), familyLabel(m.settings.Family))
	case DiscoveryVPSCountryASN:
		country := m.settings.DiscoveryCountry(m.autoCountry())
		return fmt.Sprintf("按 VPS 所在国家 ASN（%s，域名必须有 AAAA 记录）", emptyAs(country, "国家未判定"))
	}
	return fmt.Sprintf("按 VPS 入口 IP ASN（发现用 IPv4 网段，域名按 %s 校验）", familyLabel(m.settings.Family))
}

// runStatusSummary 返回最近一次扫描状态
func (m *Menu) runStatusSummary() string {
	if m.status.Phase == "" || m.status.Phase == PhaseIdle {
		if len(m.rows) == 0 {
			return m.dim("空闲：先用 1~13 调整参数，再按 s 开始扫描")
		}
		return m.dim(fmt.Sprintf("空闲（上次扫描命中 %d 个目标，按 d 查看）", len(m.rows)))
	}
	return fmt.Sprintf("%s · 命中 %d / %d 个目标 · 已扫描 %s 个 IP · 用时 %s",
		m.status.Phase.Label(), len(m.rows), m.settings.Limit, humanInt(m.status.ScannedIPs), humanDuration(m.status.Elapsed))
}

// ---- 子菜单交互 ----

// subHeader 渲染子菜单标题
func (m *Menu) subHeader(title, hint string) {
	fmt.Fprintf(m.out, "\n  %s\n", m.cyan("── "+title+" ──"))
	if hint != "" {
		fmt.Fprintf(m.out, "  %s\n", m.dim(hint))
	}
}

// chooseNumber 让用户在编号选项中选一个；返回 1 起的下标
func (m *Menu) chooseNumber(title string, options []string, current int, hint string) (int, bool) {
	for {
		m.subHeader(title, hint)
		for i, option := range options {
			mark := m.dim(" ")
			if i+1 == current {
				mark = m.green("当前")
			}
			fmt.Fprintf(m.out, "    %s %s  %s\n", m.green(pad(fmt.Sprintf("[%d]", i+1), 6)), pad(option, 24), mark)
		}
		line, ok := m.readLine("  请输入编号（回车保留当前，0 返回）: ")
		if !ok {
			return 0, false
		}
		line = strings.TrimSpace(line)
		if line == "" || line == "0" || strings.EqualFold(line, "b") {
			return 0, false
		}
		picked, err := strconv.Atoi(line)
		if err != nil || picked < 1 || picked > len(options) {
			fmt.Fprintf(m.out, "  %s\n", m.red(fmt.Sprintf("编号无效：请输入 1~%d，或回车 / 0 返回", len(options))))
			continue
		}
		return picked, true
	}
}

// inputInt 读取一个范围内的整数；回车保留当前值，b 返回
func (m *Menu) inputInt(title, hint string, current, min, max int) (int, bool) {
	for {
		m.subHeader(title, hint)
		line, ok := m.readLine(fmt.Sprintf("  请输入数值（%d~%d，回车保留 %d，b 返回）: ", min, max, current))
		if !ok {
			return 0, false
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "b") {
			return 0, false
		}
		value, err := strconv.Atoi(line)
		if err != nil || value < min || value > max {
			fmt.Fprintf(m.out, "  %s\n", m.red(fmt.Sprintf("取值无效：请输入 %d~%d 之间的整数", min, max)))
			continue
		}
		return value, true
	}
}

// inputCountry 读取两位国家/地区代码；回车保留当前值，- 清空，b 返回
func (m *Menu) inputCountry() (string, bool) {
	auto := m.autoCountry()
	hint := "两位 ISO 代码，如 DE / JP；回车保留当前，输入 - 清空，b 返回"
	for {
		m.subHeader("国家/地区代码", hint)
		value := emptyAs(m.settings.Country, m.dim("未设置"))
		fmt.Fprintf(m.out, "  当前：%s", value)
		if auto != "" {
			fmt.Fprintf(m.out, "    出口探测到的 VPS 所在国家：%s", auto)
		}
		fmt.Fprint(m.out, "\n")
		line, ok := m.readLine("  请输入国家/地区代码: ")
		if !ok {
			return "", false
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "b") {
			return "", false
		}
		if line == "-" {
			return "", true
		}
		code := strings.ToUpper(line)
		if !asn.IsCountryCode(code) {
			fmt.Fprintf(m.out, "  %s\n", m.red("代码无效：必须是两位 ISO 国家/地区代码，如 DE、JP"))
			continue
		}
		return code, true
	}
}

// editToggle 切换一个布尔开关
func (m *Menu) editToggle(title, hint string, current bool, target *bool) {
	position := 2
	if current {
		position = 1
	}
	picked, ok := m.chooseNumber(title, []string{"是", "否"}, position, hint)
	if !ok {
		return
	}
	*target = picked == 1
	m.afterEdit(fmt.Sprintf("%s 已设为 %s", title, yesNo(*target)))
}

// ---- 参数编辑 ----

func (m *Menu) editMode() {
	if m.settings.VerifyByIPv6Only() {
		m.noticef("warn", "「校验协议族」为仅 IPv6 时发现源固定为 VPS 所在国家 ASN；请先把 [2] 切回 IPv4 / 双栈再改扫描模式")
		return
	}
	options := []string{"按 VPS 入口 IP ASN", "按国家/地区 ASN"}
	current := 1
	if m.settings.Mode == ModeCountry {
		current = 2
	}
	picked, ok := m.chooseNumber("扫描模式", options, current, "按国家/地区 ASN 需要先在 [3] 填写两位国家代码")
	if !ok {
		return
	}
	if picked == 1 {
		m.settings.Mode = ModeEntry
	} else {
		m.settings.Mode = ModeCountry
	}
	m.afterEdit("扫描模式已设为 " + options[picked-1])
}

func (m *Menu) editFamily() {
	options := []string{"仅 IPv4", "仅 IPv6", "IPv4 + IPv6"}
	order := []FamilyChoice{FamilyV4, FamilyV6, FamilyBoth}
	current := 1
	for i, item := range order {
		if item == m.settings.Family {
			current = i + 1
			break
		}
	}
	picked, ok := m.chooseNumber("校验协议族", options, current,
		"协议族只作用于域名校验；选「仅 IPv6」时域名必须有 AAAA 记录，发现源改为 VPS 所在国家 ASN")
	if !ok {
		return
	}
	m.settings.Family = order[picked-1]
	if m.settings.VerifyByIPv6Only() && !HasUsableIPv6Egress(m.entries) && len(m.entries) > 0 {
		m.settings.Normalize()
		m.noticef("warn", "本机没有可用 IPv6 出口：仅 IPv6 校验会阻止扫描，可按 r 重新探测或改回 IPv4 / 双栈")
		if err := m.settings.Save(m.settingsPath); err != nil {
			m.noticef("warn", "校验协议族已设为"+options[picked-1]+"；设置保存失败："+shortError(err))
		}
		return
	}
	m.afterEdit("校验协议族已设为 " + options[picked-1])
}

func (m *Menu) editCountry() {
	code, ok := m.inputCountry()
	if !ok {
		return
	}
	m.settings.Country = code
	if code == "" {
		m.afterEdit("国家/地区代码已清空（恢复自动判定）")
		return
	}
	m.afterEdit("国家/地区代码已设为 " + code)
}

func (m *Menu) editLimit() {
	value, ok := m.inputInt("目标数量", "命中达到该数量立即停止；常用档位 1 / 3 / 5 / 10 / 20 / 50，上限 100",
		m.settings.Limit, MinLimit, MaxLimit)
	if !ok {
		return
	}
	m.settings.Limit = value
	m.afterEdit(fmt.Sprintf("目标数量已设为 %d", value))
}

func (m *Menu) editThreads() {
	value, ok := m.inputInt("扫描并发", "单机 1 核建议不超过 200；死 IP 多时可降低并发减少误判",
		m.settings.Threads, MinThreads, MaxThreads)
	if !ok {
		return
	}
	m.settings.Threads = value
	m.afterEdit(fmt.Sprintf("扫描并发已设为 %d", value))
}

func (m *Menu) editTimeout() {
	value, ok := m.inputInt("单 IP 超时", "单位：秒；死 IP 多时调小可加速熔断，跨境线路可调大",
		m.settings.TimeoutSec, MinTimeout, MaxTimeout)
	if !ok {
		return
	}
	m.settings.TimeoutSec = value
	m.afterEdit(fmt.Sprintf("单 IP 超时已设为 %ds", value))
}

func (m *Menu) editMinStars() {
	value, ok := m.inputInt("最低星级", "0 = 不限；星级由证书、握手延迟与页面质量综合计算",
		m.settings.Filter.MinStars, 0, 5)
	if !ok {
		return
	}
	m.settings.Filter.MinStars = value
	m.afterEdit("最低星级已设为 " + optionalCount(value, " 星"))
}

func (m *Menu) editHandshake() {
	current := int(m.settings.Filter.MaxHandshakeMS)
	value, ok := m.inputInt("握手延迟上限", "单位：毫秒；0 = 不限；超过该值的候选会被淘汰",
		current, 0, 5000)
	if !ok {
		return
	}
	m.settings.Filter.MaxHandshakeMS = int64(value)
	m.afterEdit("握手延迟上限已设为 " + optionalMS(int64(value)))
}

func (m *Menu) editCertDays() {
	value, ok := m.inputInt("证书剩余天数", "0 = 不限；剔除证书临近到期的站点",
		m.settings.Filter.MinCertDays, 0, 3650)
	if !ok {
		return
	}
	m.settings.Filter.MinCertDays = value
	m.afterEdit("证书剩余天数已设为 " + optionalCount(value, " 天"))
}

func (m *Menu) editAllowCDN() {
	m.editToggle("允许 CDN", "默认否：避免让 VPS 沦为公网免费反代",
		m.settings.Filter.AllowCDN, &m.settings.Filter.AllowCDN)
}

func (m *Menu) editAllowHot() {
	m.editToggle("允许热门大站", "默认否：避开 Google / Apple 这类超大站",
		m.settings.Filter.AllowHot, &m.settings.Filter.AllowHot)
}

func (m *Menu) editExcludeCN() {
	m.editToggle("排除国内站点", "默认是：等价于 require_no_cn",
		m.settings.Filter.ExcludeCN, &m.settings.Filter.ExcludeCN)
}

func (m *Menu) editExcludeDefault() {
	m.editToggle("排除默认页", "默认是：剔除 Nginx / OpenResty 等默认欢迎页",
		m.settings.Filter.ExcludeDefaultPage, &m.settings.Filter.ExcludeDefaultPage)
}

// afterEdit 归一化并持久化参数，然后给出一行提示
func (m *Menu) afterEdit(message string) {
	m.settings.Normalize()
	if err := m.settings.Save(m.settingsPath); err != nil {
		m.noticef("warn", message+"；设置保存失败："+shortError(err))
		return
	}
	m.noticef("ok", message+"（已保存到 "+m.settingsPath+"）")
}

// ---- 扫描 ----

// scan 按当前参数执行一次扫描（阻塞直到完成或被 Ctrl+C 停止）
func (m *Menu) scan() {
	if m.probing {
		m.noticef("warn", "正在探测本机出口，请稍候再开始扫描")
		return
	}
	if reason := m.settings.Validate(); reason != "" {
		m.noticef("warn", reason)
		return
	}
	plan, reason := m.buildPlan()
	if plan == nil {
		m.noticef("err", reason)
		return
	}
	runner := NewRunner(RunnerConfig{
		Engine:  m.deps.Engine,
		Scanner: m.deps.Scanner,
		Filter:  m.settings.FilterConfig(m.deps.BaseCfg.Filter),
		Limit:   m.settings.Limit,
		Family:  m.settings.DialFamily(),
	})

	// Ctrl+C 只停止本次扫描并回到菜单，不退出面板
	ctx, stop := signal.NotifyContext(m.ctx, os.Interrupt)
	defer stop()

	m.clearScreen()
	fmt.Fprintf(m.out, "\n  %s\n\n", m.cyan(fmt.Sprintf("开始扫描：%s（目标数量 %d，按 Ctrl+C 停止）",
		plan.Summary(), m.settings.Limit)))

	rows, status := RunHeadless(ctx, m.settings, plan, runner, 2*time.Second)
	m.rows, m.status = rows, status

	switch status.Phase {
	case PhaseFailed:
		m.noticef("err", fmt.Sprintf("扫描失败：%s（%s）", status.Message, emptyAs(status.Reason, "无附加原因")))
	case PhaseStopped:
		m.noticef("warn", fmt.Sprintf("扫描已停止：命中 %d 个目标（按 d 查看结果表）", len(rows)))
	default:
		m.noticef("ok", fmt.Sprintf("扫描结束：命中 %d 个目标（按 d 查看结果表）", len(rows)))
	}
	m.pause("  按回车返回主菜单…")
}

// showResults 打印上次扫描结果表
func (m *Menu) showResults() {
	m.clearScreen()
	fmt.Fprintf(m.out, "\n  %s\n", m.cyan("【上次扫描结果】"))
	if m.status.Phase == "" || m.status.Phase == PhaseIdle {
		fmt.Fprintf(m.out, "  %s\n", m.yellow("本次会话还没有扫描记录：按 s 开始扫描"))
		m.pause("  按回车返回主菜单…")
		return
	}

	fmt.Fprintf(m.out, "  %s\n", fmt.Sprintf("%s · 命中 %d / %d 个目标 · 已扫描 %s 个 IP · 用时 %s",
		m.status.Phase.Label(), len(m.rows), m.settings.Limit,
		humanInt(m.status.ScannedIPs), humanDuration(m.status.Elapsed)))
	if m.status.Rejected > 0 {
		fmt.Fprintf(m.out, "  %s\n", m.dim(fmt.Sprintf("剔除候选 %d 个（最近：%s — %s）",
			m.status.Rejected, emptyAs(m.status.LastRejectDomain, "—"), emptyAs(m.status.LastReject, "—"))))
	}

	if len(m.rows) == 0 {
		fmt.Fprintf(m.out, "  %s\n", m.yellow("本次没有命中符合条件的目标：可调低星级 / 放宽握手延迟或证书限制后重试"))
		m.pause("  按回车返回主菜单…")
		return
	}

	header := fmt.Sprintf("%s %s %s %s %s %s %s %s %s", pad("域名", 30), pad("IP", 16), pad("ASN", 10),
		pad("国家", 6), pad("星级", 5), pad("握手", 8), pad("证书天", 7), pad("CDN", 5), "状态码")
	fmt.Fprintf(m.out, "  %s\n", m.dim(header))
	for _, row := range m.rows {
		fmt.Fprintf(m.out, "  %s %s %s %s %s %s %s %s %d\n",
			pad(row.Domain, 30),
			pad(row.IP, 16),
			pad(emptyAs(row.ASN, "—"), 10),
			pad(emptyAs(row.Country, "—"), 6),
			pad(fmt.Sprintf("%d 星", row.Stars), 5),
			pad(fmt.Sprintf("%d ms", row.HandshakeMS), 8),
			pad(fmt.Sprintf("%d", row.CertDays), 7),
			pad(yesNo(row.IsCDN), 5),
			row.StatusCode)
	}
	m.pause("  按回车返回主菜单…")
}

// buildPlan 依据当前参数生成扫描计划（发现源由 DiscoveryKind 决定，协议族只作用于域名校验）
func (m *Menu) buildPlan() (Plan, string) {
	if m.deps.Engine == nil || m.deps.Scanner == nil {
		return nil, "扫描引擎未初始化"
	}
	if reason := StartBlocker(m.settings, m.entries, m.probing); reason != "" {
		return nil, reason
	}
	switch m.settings.DiscoveryKind() {
	case DiscoveryCountryASN:
		if m.deps.Country == nil {
			return nil, "国家 ASN 查询客户端未初始化"
		}
		if m.settings.Country == "" {
			return nil, "按国家/地区 ASN 扫描需要先填写国家代码（[3]）"
		}
		return NewCountryPlan(m.deps.Country, m.settings.Country, QueueCountryASN), ""
	case DiscoveryVPSCountryASN:
		if m.deps.Country == nil {
			return nil, "国家 ASN 查询客户端未初始化"
		}
		country := m.settings.DiscoveryCountry(m.autoCountry())
		if country == "" {
			return nil, "无法判定 VPS 所在国家（出口探测失败）：按 r 重新探测，或在 [3] 手动填写国家代码"
		}
		return NewCountryPlan(m.deps.Country, country, QueueVPSCountryASN), ""
	default:
		return NewEntryPlan(m.entries, m.settings.Family), ""
	}
}

// probeEntries 探测本机 IPv4/IPv6 出口（阻塞，最长 45 秒）
func (m *Menu) probeEntries() {
	m.probing = true
	m.notice = ""
	fmt.Fprintf(m.out, "\n  %s\n", m.dim("正在探测本机出口 IPv4/IPv6…（最长 45 秒）"))
	ctx, cancel := context.WithTimeout(m.ctx, 45*time.Second)
	options := m.deps.Probe(ctx, m.deps.Reader, m.deps.Resolver)
	cancel()
	m.entries = options
	m.probing = false

	available := 0
	for _, option := range options {
		if option.Available {
			available++
		}
	}
	switch {
	case len(options) == 0:
		m.noticef("warn", "出口探测没有返回结果：可按 r 重试，或改用「按国家/地区 ASN」模式")
	case available == 0:
		m.noticef("warn", "IPv4/IPv6 出口均不可用：可改用「按国家/地区 ASN」模式（[1]）")
	default:
		m.noticef("ok", fmt.Sprintf("出口探测完成：%d 个可用出口，按 s 开始扫描", available))
	}
}

// autoCountry 返回出口探测推断的 VPS 所在国家（优先 IPv6 出口，回退 IPv4）
func (m *Menu) autoCountry() string {
	return AutoDiscoveryCountry(m.entries)
}

// ---- 维护 ----

// checkUpdate 查询最新版本，确认后下载并替换当前程序
func (m *Menu) checkUpdate() {
	updater := m.updater
	if updater == nil {
		updater = NewUpdater()
	}
	m.clearScreen()
	fmt.Fprintf(m.out, "\n  %s\n", m.cyan("【检查更新】"))
	fmt.Fprintf(m.out, "  %s\n", m.dim(fmt.Sprintf("当前版本 v%s，正在查询 GitHub Releases…", normalizeVersion(updater.Current))))

	ctx, cancel := context.WithTimeout(m.ctx, 20*time.Second)
	info, err := updater.Check(ctx)
	cancel()
	if err != nil {
		m.noticef("err", "检查更新失败："+shortError(err))
		return
	}
	if !info.Available {
		m.noticef("ok", fmt.Sprintf("已是最新版本 v%s", info.Current))
		return
	}

	fmt.Fprintf(m.out, "\n  当前版本  %s\n  最新版本  %s\n  下载地址  %s\n",
		"v"+info.Current, m.green("v"+info.Latest), m.dim(info.URL))
	if info.Notes != "" {
		fmt.Fprintf(m.out, "\n  %s\n", m.dim("更新说明："))
		for index, line := range strings.Split(info.Notes, "\n") {
			if index >= 6 {
				fmt.Fprintf(m.out, "    %s\n", m.dim("…"))
				break
			}
			fmt.Fprintf(m.out, "    %s\n", m.dim(strings.TrimSpace(line)))
		}
	}

	line, ok := m.readLine("\n  是否下载并安装？下载完成后自动替换当前程序 [y/N]: ")
	if !ok {
		m.quit = true
		return
	}
	if !isAffirmative(line) {
		m.noticef("warn", "已取消更新")
		return
	}

	fmt.Fprintf(m.out, "  %s\n", m.dim("正在下载、校验并替换主程序…"))
	applyCtx, applyCancel := context.WithTimeout(m.ctx, 10*time.Minute)
	defer applyCancel()
	if err := updater.Apply(applyCtx, info); err != nil {
		m.noticef("err", "更新失败："+shortError(err)+"（可重跑 install.sh 覆盖安装）")
		return
	}
	m.noticef("ok", fmt.Sprintf("已更新到 v%s：按 0 退出后重新运行 rcm 生效", info.Latest))
}

// uninstallPanel 按清单卸载程序（需二次确认），完成后退出面板
func (m *Menu) uninstallPanel() {
	targets := BuildUninstallTargets(m.uninstall)
	if len(targets) == 0 {
		m.noticef("warn", "未找到可卸载的安装文件（可能已手动清理）")
		return
	}
	basic, deep := splitUninstallTargets(targets)

	m.clearScreen()
	fmt.Fprintf(m.out, "\n  %s\n\n", m.cyan("【卸载 Reality Checker】"))
	fmt.Fprintf(m.out, "  %s\n", m.yellow("以下内容将被删除："))
	for _, target := range basic {
		fmt.Fprintf(m.out, "    %s %s\n", pad(target.Label, 20), m.dim(target.Path))
	}
	if len(deep) > 0 {
		fmt.Fprintf(m.out, "\n  %s\n", m.yellow("彻底卸载还会删除配置与离线规则库："))
		for _, target := range deep {
			fmt.Fprintf(m.out, "    %s %s\n", pad(target.Label, 20), m.dim(target.Path))
		}
	}
	fmt.Fprintf(m.out, "\n  %s\n", m.dim("用户数据（如 data/reality_targets.db）与 systemd 守护进程不会被删除；卸载不可撤销。"))

	line, ok := m.readLine("\n  输入 1 仅卸载程序 / 2 彻底卸载 / 其他取消: ")
	if !ok {
		m.quit = true
		return
	}
	var selected []UninstallTarget
	switch strings.TrimSpace(line) {
	case "1":
		selected = basic
	case "2":
		selected = append(selected, basic...)
		selected = append(selected, deep...)
	default:
		m.noticef("warn", "已取消卸载")
		return
	}

	// 面板通常就在安装目录里运行：先离开当前目录，避免删除后仍引用它
	if installDir := strings.TrimSpace(m.uninstall.InstallDir); installDir != "" {
		if cwd, err := os.Getwd(); err == nil && isWithinDir(cwd, installDir) {
			if err := os.Chdir(os.TempDir()); err == nil {
				defer func() { _ = os.Chdir(cwd) }()
			}
		}
	}
	result := RemoveUninstallTargets(selected)

	fmt.Fprintf(m.out, "\n  %s\n", m.cyan("【卸载结果】"))
	for _, path := range result.Removed {
		fmt.Fprintf(m.out, "  %s 已删除 %s\n", m.green("✔"), path)
	}
	for _, failure := range result.Failed {
		fmt.Fprintf(m.out, "  %s 删除失败 %s\n", m.red("✖"), failure)
	}
	if len(result.Failed) > 0 {
		fmt.Fprintf(m.out, "  %s\n", m.yellow("部分文件删除失败：请用 root 或安装用户重试，或手动删除上面的路径。"))
	} else {
		fmt.Fprintf(m.out, "  %s\n", m.green("卸载完成。"))
	}
	m.quit = true
}

// isAffirmative 判断确认输入是否为同意
func isAffirmative(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "y", "yes", "是":
		return true
	}
	return false
}

// ---- 输入输出 ----

// readLine 输出提示并读取一行；返回 false 表示输入已结束
func (m *Menu) readLine(prompt string) (string, bool) {
	fmt.Fprint(m.out, prompt)
	line, err := m.in.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return "", false
	}
	return strings.TrimRight(line, "\r\n"), true
}

// pause 等待用户回车（阅读扫描输出 / 结果表）
func (m *Menu) pause(prompt string) {
	if _, ok := m.readLine(prompt); !ok {
		m.quit = true
	}
}

// clearScreen 清屏：真实终端用 ANSI 清屏，管道输出只留空行
func (m *Menu) clearScreen() {
	if m.tty {
		fmt.Fprint(m.out, "\x1b[H\x1b[2J")
		return
	}
	fmt.Fprint(m.out, "\n")
}

// noticef 记录一行待显示的操作提示
func (m *Menu) noticef(kind, message string) {
	m.noticeKind = kind
	m.notice = message
}

// noticeStyle 按提示类型着色
func (m *Menu) noticeStyle() string {
	text := "提示：" + m.notice
	switch m.noticeKind {
	case "ok":
		return m.green("✔ ") + m.green(text)
	case "warn":
		return m.yellow("! ") + m.yellow(text)
	case "err":
		return m.red("✖ ") + m.red(text)
	default:
		return m.dim(text)
	}
}

// cyan / green / yellow / red / dim 在终端输出时上色
func (m *Menu) cyan(text string) string   { return m.colorize(ansiCyan, text) }
func (m *Menu) green(text string) string  { return m.colorize(ansiGreen, text) }
func (m *Menu) yellow(text string) string { return m.colorize(ansiYellow, text) }
func (m *Menu) red(text string) string    { return m.colorize(ansiRed, text) }
func (m *Menu) dim(text string) string    { return m.colorize(ansiDim, text) }

// colorize 按当前输出能力决定是否包裹 ANSI 颜色
func (m *Menu) colorize(code, text string) string {
	if !m.color {
		return text
	}
	return code + text + ansiReset
}
