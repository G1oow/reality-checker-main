package panel

import (
	"context"
	"fmt"
	"strings"
	"time"

	"RealityChecker/internal/asn"
	"RealityChecker/internal/core"
	"RealityChecker/internal/scanner"
	"RealityChecker/internal/types"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oschwald/geoip2-golang"
)

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

// Focus 面板焦点区域
type Focus int

const (
	// FocusSettings 焦点在参数区
	FocusSettings Focus = iota
	// FocusResults 焦点在结果区
	FocusResults
)

type entriesProbedMsg struct{ options []EntryOption }

type tickMsg struct{ at time.Time }

// Model 面板主模型
type Model struct {
	deps         Deps
	ctx          context.Context
	settings     Settings
	settingsPath string
	settingsNote string

	entries  []EntryOption
	probing  bool
	probeErr string

	plan   Plan
	runner *Runner
	rows   []Row

	focus        Focus
	cursor       int
	resultOffset int

	editing bool
	editBuf string

	width  int
	height int

	statusMsg  string
	statusKind string
	quitting   bool
}

// NewModel 创建面板模型
func NewModel(ctx context.Context, deps Deps, settings Settings, settingsPath string) *Model {
	if ctx == nil {
		ctx = context.Background()
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Probe == nil {
		deps.Probe = ProbeEntries
	}
	if settingsPath == "" {
		settingsPath = DefaultSettingsPath()
	}
	settings.Normalize()

	model := &Model{
		deps:         deps,
		ctx:          ctx,
		settings:     settings,
		settingsPath: settingsPath,
		width:        110,
		height:       34,
		statusMsg:    "按 s 开始扫描，x 停止，q 退出",
		statusKind:   "info",
	}
	return model
}

// Init 初始化面板（首帧开始探测入口 IP）
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.probeEntriesCmd(), tea.Tick(400*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg{at: t}
	}))
}

// Settings 返回当前面板参数
func (m *Model) Settings() Settings { return m.settings }

// Rows 返回当前命中结果
func (m *Model) Rows() []Row { return m.rows }

// Status 返回扫描状态
func (m *Model) Status() Status {
	if m.runner == nil {
		return Status{Phase: PhaseIdle, Message: m.statusMsg}
	}
	return m.runner.Status()
}

// SettingsPath 返回设置文件路径
func (m *Model) SettingsPath() string { return m.settingsPath }

// SettingsNote 返回设置持久化提示
func (m *Model) SettingsNote() string { return m.settingsNote }

// SetEntries 注入入口探测结果（测试与外部复用）
func (m *Model) SetEntries(options []EntryOption) {
	m.entries = options
	m.probing = false
}

// Update 处理按键与异步消息
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
		return m, nil

	case entriesProbedMsg:
		m.probing = false
		m.entries = typed.options
		m.probeErr = ""
		unavailable := 0
		for _, option := range typed.options {
			if !option.Available {
				unavailable++
			}
		}
		if unavailable == len(typed.options) && len(typed.options) > 0 {
			m.setStatus("warn", "入口探测完成：IPv4/IPv6 出口均不可用，可改用「按国家/地区 ASN」模式")
		} else {
			m.setStatus("info", "入口探测完成：按 s 开始扫描")
		}
		return m, nil

	case tickMsg:
		if m.runner != nil {
			m.rows = m.runner.Rows()
			status := m.runner.Status()
			if status.Phase == PhaseDone || status.Phase == PhaseStopped || status.Phase == PhaseFailed {
				m.setStatus(kindForPhase(status.Phase), fmt.Sprintf("%s：%s", status.Phase.Label(), status.Message))
			}
		}
		return m, tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg {
			return tickMsg{at: t}
		})

	case tea.KeyMsg:
		return m.handleKey(typed)
	}
	return m, nil
}

// handleKey 处理按键
func (m *Model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.editing {
		return m.handleEditKey(key)
	}

	switch key.String() {
	case "ctrl+c", "q":
		m.quitting = true
		if m.runner != nil {
			m.runner.Stop()
		}
		return m, tea.Quit
	case "esc":
		if m.runner != nil && m.runner.Running() {
			m.runner.Stop()
			m.setStatus("warn", "正在停止扫描…")
		}
		return m, nil
	case "tab":
		if m.focus == FocusSettings {
			m.focus = FocusResults
		} else {
			m.focus = FocusSettings
		}
		return m, nil
	case "s":
		return m.startScan()
	case "x":
		if m.runner != nil && m.runner.Running() {
			m.runner.Stop()
			m.setStatus("warn", "正在停止扫描…")
		} else {
			m.setStatus("info", "当前没有正在进行的扫描")
		}
		return m, nil
	case "r":
		if m.runner != nil && m.runner.Running() {
			m.setStatus("warn", "扫描进行中，无法重新探测入口")
			return m, nil
		}
		m.probing = true
		m.setStatus("info", "正在重新探测本机出口 IPv4/IPv6…")
		return m, m.probeEntriesCmd()
	case "up", "k":
		m.moveCursor(-1)
		return m, nil
	case "down", "j":
		m.moveCursor(1)
		return m, nil
	case "left", "h":
		return m.adjust(-1)
	case "right", "l":
		return m.adjust(1)
	case " ":
		return m.adjust(1)
	case "enter":
		if m.cursor == rowCountry {
			m.editing = true
			m.editBuf = m.settings.Country
			return m, nil
		}
		return m.adjust(1)
	}
	return m, nil
}

// handleEditKey 处理文本编辑按键
func (m *Model) handleEditKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "enter":
		m.editing = false
		m.settings.Country = strings.ToUpper(strings.TrimSpace(m.editBuf))
		m.settings.Normalize()
		if m.settings.Country == "" {
			m.setStatus("warn", "国家/地区代码已清空")
		} else {
			m.setStatus("info", fmt.Sprintf("国家/地区代码已设为 %s", m.settings.Country))
		}
		m.persist()
		return m, nil
	case "esc", "ctrl+c":
		m.editing = false
		m.setStatus("info", "已取消输入")
		return m, nil
	case "backspace":
		if len(m.editBuf) > 0 {
			m.editBuf = m.editBuf[:len(m.editBuf)-1]
		}
		return m, nil
	}
	if len(key.Runes) > 0 {
		for _, r := range key.Runes {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				m.editBuf += string(r)
			}
		}
		if len(m.editBuf) > 2 {
			m.editBuf = m.editBuf[len(m.editBuf)-2:]
		}
	}
	return m, nil
}

// moveCursor 在设置区或结果区移动光标
func (m *Model) moveCursor(delta int) {
	if m.focus == FocusResults {
		m.resultOffset += delta
		if m.resultOffset < 0 {
			m.resultOffset = 0
		}
		maxOffset := len(m.rows) - 1
		if maxOffset < 0 {
			maxOffset = 0
		}
		if m.resultOffset > maxOffset {
			m.resultOffset = maxOffset
		}
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= rowCount {
		m.cursor = rowCount - 1
	}
	if m.cursor == rowCountry && m.settings.Mode != ModeCountry {
		// 入口模式下国家代码不参与，跳过到下一行
		if delta > 0 {
			m.cursor = rowLimit
		} else {
			m.cursor = rowFamily
		}
	}
}

// adjust 修改当前设置行的取值
func (m *Model) adjust(delta int) (tea.Model, tea.Cmd) {
	m.settings.Normalize()
	switch m.cursor {
	case rowMode:
		if m.settings.Mode == ModeEntry {
			m.settings.Mode = ModeCountry
		} else {
			m.settings.Mode = ModeEntry
		}
	case rowFamily:
		m.settings.Family = stepFamily(m.settings.Family, delta)
	case rowCountry:
		if m.settings.Mode == ModeCountry {
			m.editing = true
			m.editBuf = m.settings.Country
			return m, nil
		}
	case rowLimit:
		m.settings.Limit = cycleInt(LimitChoices, m.settings.Limit, delta)
	case rowThreads:
		m.settings.Threads = cycleInt(ThreadChoices, m.settings.Threads, delta)
	case rowTimeout:
		m.settings.TimeoutSec = cycleInt(TimeoutChoices, m.settings.TimeoutSec, delta)
	case rowMinStars:
		m.settings.Filter.MinStars = clampInt(m.settings.Filter.MinStars+delta, 0, 5)
	case rowMaxHandshake:
		m.settings.Filter.MaxHandshakeMS = cycleInt64(handshakeChoices, m.settings.Filter.MaxHandshakeMS, delta)
	case rowMinCertDays:
		m.settings.Filter.MinCertDays = clampInt(m.settings.Filter.MinCertDays+delta*5, 0, 365)
	case rowAllowCDN:
		m.settings.Filter.AllowCDN = !m.settings.Filter.AllowCDN
	case rowAllowHot:
		m.settings.Filter.AllowHot = !m.settings.Filter.AllowHot
	case rowExcludeCN:
		m.settings.Filter.ExcludeCN = !m.settings.Filter.ExcludeCN
	case rowExcludeDefault:
		m.settings.Filter.ExcludeDefaultPage = !m.settings.Filter.ExcludeDefaultPage
	}
	m.settings.Normalize()
	m.persist()
	return m, nil
}

// handshakeChoices 握手延迟上限档位（毫秒，0 = 不限）
var handshakeChoices = []int64{0, 100, 200, 300, 500, 800, 1000, 1500, 2000}

// startScan 一键开始扫描
func (m *Model) startScan() (tea.Model, tea.Cmd) {
	if m.runner != nil && m.runner.Running() {
		m.setStatus("warn", "扫描正在进行中，按 x 可停止")
		return m, nil
	}
	if reason := m.settings.Validate(); reason != "" {
		m.setStatus("err", reason)
		return m, nil
	}
	if m.deps.Engine == nil || m.deps.Scanner == nil {
		m.setStatus("err", "扫描引擎未初始化")
		return m, nil
	}

	var plan Plan
	switch m.settings.Mode {
	case ModeCountry:
		if m.deps.Country == nil {
			m.setStatus("err", "国家 ASN 查询客户端未初始化")
			return m, nil
		}
		plan = NewCountryPlan(m.deps.Country, m.settings.Country, m.settings.Family)
	default:
		if m.probing {
			m.setStatus("warn", "正在探测入口 IP，请稍候再开始")
			return m, nil
		}
		plan = NewEntryPlan(m.entries, m.settings.Family)
	}

	m.plan = plan
	m.rows = nil
	m.resultOffset = 0
	m.runner = NewRunner(RunnerConfig{
		Engine:  m.deps.Engine,
		Scanner: m.deps.Scanner,
		Filter:  m.settings.FilterConfig(m.deps.BaseCfg.Filter),
		Limit:   m.settings.Limit,
	})
	if err := m.runner.Start(m.ctx, plan); err != nil {
		m.setStatus("err", err.Error())
		return m, nil
	}
	m.focus = FocusResults
	m.setStatus("info", fmt.Sprintf("%s，目标数量 %d，按 x 可停止", plan.Summary(), m.settings.Limit))
	return m, nil
}

// persist 保存面板参数到本地设置文件
func (m *Model) persist() {
	if err := m.settings.Save(m.settingsPath); err != nil {
		m.settingsNote = "设置保存失败：" + shortError(err)
		return
	}
	m.settingsNote = "设置已保存至 " + m.settingsPath
}

// probeEntriesCmd 异步探测入口 IP
func (m *Model) probeEntriesCmd() tea.Cmd {
	deps := m.deps
	ctx := m.ctx
	return func() tea.Msg {
		probeCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		return entriesProbedMsg{options: deps.Probe(probeCtx, deps.Reader, deps.Resolver)}
	}
}

// setStatus 更新状态提示
func (m *Model) setStatus(kind, message string) {
	m.statusKind = kind
	m.statusMsg = message
}

// kindForPhase 把扫描阶段映射为提示样式
func kindForPhase(phase Phase) string {
	switch phase {
	case PhaseDone:
		return "ok"
	case PhaseStopped:
		return "warn"
	case PhaseFailed:
		return "err"
	default:
		return "info"
	}
}

// 设置行索引
const (
	rowMode = iota
	rowFamily
	rowCountry
	rowLimit
	rowThreads
	rowTimeout
	rowMinStars
	rowMaxHandshake
	rowMinCertDays
	rowAllowCDN
	rowAllowHot
	rowExcludeCN
	rowExcludeDefault
	rowCount
)

// settingRow 一行可编辑参数
type settingRow struct {
	Label string
	Value string
}

// settingRows 返回按顺序排列的参数行（含当前取值）
func (m *Model) settingRows() []settingRow {
	mode := "按 VPS 入口 IP ASN"
	if m.settings.Mode == ModeCountry {
		mode = "按国家/地区 ASN"
	}
	country := m.settings.Country
	if country == "" {
		if m.settings.Mode == ModeCountry {
			country = "未设置（回车输入）"
		} else {
			country = "仅国家模式使用"
		}
	}
	rows := []settingRow{
		{"扫描模式", mode},
		{"入口协议族", familyLabel(m.settings.Family)},
		{"国家/地区代码", country},
		{"目标数量 limit", fmt.Sprintf("%d", m.settings.Limit)},
		{"扫描并发", fmt.Sprintf("%d", m.settings.Threads)},
		{"单 IP 超时", fmt.Sprintf("%ds", m.settings.TimeoutSec)},
		{"最低星级", optionalCount(m.settings.Filter.MinStars, " 星")},
		{"握手延迟上限", optionalMS(m.settings.Filter.MaxHandshakeMS)},
		{"证书剩余天数", optionalCount(m.settings.Filter.MinCertDays, " 天")},
		{"允许 CDN", yesNo(m.settings.Filter.AllowCDN)},
		{"允许热门大站", yesNo(m.settings.Filter.AllowHot)},
		{"排除国内站点", yesNo(m.settings.Filter.ExcludeCN)},
		{"排除默认页", yesNo(m.settings.Filter.ExcludeDefaultPage)},
	}
	return rows
}

// familyLabel 返回协议族选择的显示名
func familyLabel(choice FamilyChoice) string {
	switch choice {
	case FamilyV4:
		return "仅 IPv4"
	case FamilyV6:
		return "仅 IPv6"
	default:
		return "IPv4 + IPv6"
	}
}

func yesNo(value bool) string {
	if value {
		return "是"
	}
	return "否"
}

func optionalCount(value int, unit string) string {
	if value <= 0 {
		return "不限"
	}
	return fmt.Sprintf("%d%s", value, unit)
}

func optionalMS(value int64) string {
	if value <= 0 {
		return "不限"
	}
	return fmt.Sprintf("%d ms", value)
}

// stepFamily 在协议族之间按档位移动（到边界即停）
func stepFamily(current FamilyChoice, delta int) FamilyChoice {
	order := []FamilyChoice{FamilyV4, FamilyV6, FamilyBoth}
	index := 0
	for i, item := range order {
		if item == current {
			index = i
			break
		}
	}
	index = clampInt(index+delta, 0, len(order)-1)
	return order[index]
}

// cycleInt 在档位列表中按档位移动（到边界即停）
func cycleInt(choices []int, current, delta int) int {
	if len(choices) == 0 {
		return current
	}
	index := nearestIndex(choices, current)
	index = clampInt(index+delta, 0, len(choices)-1)
	return choices[index]
}

// cycleInt64 在 int64 档位列表中按档位移动（到边界即停）
func cycleInt64(choices []int64, current int64, delta int) int64 {
	if len(choices) == 0 {
		return current
	}
	order := make([]int, 0, len(choices))
	for _, item := range choices {
		order = append(order, int(item))
	}
	return int64(cycleInt(order, int(current), delta))
}

// nearestIndex 找到最接近当前值的档位下标
func nearestIndex(choices []int, current int) int {
	best := 0
	bestDiff := -1
	for i, item := range choices {
		diff := item - current
		if diff < 0 {
			diff = -diff
		}
		if bestDiff < 0 || diff < bestDiff {
			best = i
			bestDiff = diff
		}
	}
	return best
}
