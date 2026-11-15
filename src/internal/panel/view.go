package panel

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	labelStyle = lipgloss.NewStyle().Bold(true)
	dimStyle   = lipgloss.NewStyle().Faint(true)
	cursorMark = lipgloss.NewStyle().Bold(true)
	okStyle    = lipgloss.NewStyle().Bold(true)
	warnStyle  = lipgloss.NewStyle().Bold(true)
	errStyle   = lipgloss.NewStyle().Bold(true)
)

var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

// StripANSI 去掉 ANSI 转义序列（供测试与日志复用）
func StripANSI(text string) string {
	return ansiPattern.ReplaceAllString(text, "")
}

// View 渲染面板
func (m *Model) View() string {
	if m.quitting {
		return "面板已退出。\n"
	}

	width := m.width
	if width <= 0 {
		width = 110
	}
	narrow := width < 96

	lines := []string{m.renderHeader(), ""}
	settingsBlock := m.renderSettings()
	statusBlock := m.renderStatus()

	if narrow {
		lines = append(lines, settingsBlock)
		lines = append(lines, "")
		lines = append(lines, statusBlock)
	} else {
		leftWidth := 46
		rightWidth := width - leftWidth - 6
		if rightWidth < 46 {
			rightWidth = 46
		}
		left := lipgloss.NewStyle().Width(leftWidth).Render(settingsBlock)
		right := lipgloss.NewStyle().Width(rightWidth).Render(statusBlock)
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, left, "   ", right))
	}

	lines = append(lines, "")
	lines = append(lines, m.renderResults())
	lines = append(lines, "")
	lines = append(lines, m.renderFooter())
	return strings.Join(lines, "\n")
}

// renderHeader 渲染标题行
func (m *Model) renderHeader() string {
	title := titleStyle.Render("R-C-M 面板")
	tag := dimStyle.Render("按国家/地区 ASN 或 VPS 入口 IP ASN 扫描 REALITY 目标")
	keys := dimStyle.Render("s 开始扫描 · x/esc 停止 · r 重探入口 · ↑↓ 选择 · ←→ 改值 · tab 切换焦点 · q 退出")
	return fmt.Sprintf("%s  %s\n%s", title, tag, keys)
}

// renderSettings 渲染参数区
func (m *Model) renderSettings() string {
	rows := m.settingRows()
	var builder strings.Builder
	builder.WriteString(labelStyle.Render("扫描参数"))
	builder.WriteString("\n")

	const labelWidth = 16
	for i, row := range rows {
		marker := "  "
		if m.focus == FocusSettings && i == m.cursor {
			if m.editing && i == rowCountry {
				marker = cursorMark.Render("› ")
			} else {
				marker = cursorMark.Render("› ")
			}
		}
		value := row.Value
		if i == rowCountry && m.editing {
			value = "输入中：" + m.editBuf + "_"
		}
		if row.Label == "国家/地区代码" && m.settings.Mode != ModeCountry {
			value = dimStyle.Render(value)
		}
		builder.WriteString(fmt.Sprintf("%s%s %s\n", marker, pad(row.Label, labelWidth), value))
	}
	return builder.String()
}

// renderStatus 渲染入口与进度区
func (m *Model) renderStatus() string {
	var builder strings.Builder
	builder.WriteString(labelStyle.Render("入口与进度"))
	builder.WriteString("\n")

	builder.WriteString(m.renderEntries())

	status := m.Status()
	progress := status.Progress()
	progressText := "未知"
	if progress >= 0 {
		progressText = fmt.Sprintf("%.0f%%", progress)
	}
	builder.WriteString(fmt.Sprintf("  阶段     %s（%s）\n", status.Phase.Label(), progressText))
	builder.WriteString(fmt.Sprintf("  已扫描   %s 个 IP · 速率 %s\n", humanInt(status.ScannedIPs), humanRate(status.Rate)))
	builder.WriteString(fmt.Sprintf("  命中     %d / %d 个目标 · 候选域名 %d\n", status.Hits, m.settings.Limit, status.Candidates))
	builder.WriteString(fmt.Sprintf("  当前网段 %s\n", emptyAs(status.CurrentCIDR, "—")))
	builder.WriteString(fmt.Sprintf("  当前 IP  %s\n", emptyAs(status.CurrentIP, "—")))
	if status.BatchTotal > 0 {
		builder.WriteString(fmt.Sprintf("  批次     %s  %d / %d %s\n",
			emptyAs(status.BatchLabel, "—"), status.BatchIndex, status.BatchTotal, status.Plan.QueueName))
	}
	builder.WriteString(fmt.Sprintf("  耗时     %s\n", humanDuration(status.Elapsed)))

	if stats := status.Plan; stats.Country != "" {
		builder.WriteString(fmt.Sprintf("  国家清单 %s：%d 个 ASN（IPv4 网段 %d / IPv6 网段 %d）\n",
			stats.Country, stats.CountryASNs, stats.CountryIPv4, stats.CountryIPv6))
	}
	if status.Plan.Note != "" {
		builder.WriteString("  " + warnStyle.Render(status.Plan.Note) + "\n")
	}
	for _, skipped := range status.Plan.Skipped {
		builder.WriteString("  " + dimStyle.Render("跳过 "+skipped) + "\n")
	}
	if status.Message != "" {
		builder.WriteString("  " + status.Message + "\n")
	}
	if status.Reason == "limit_hit" {
		builder.WriteString("  " + okStyle.Render("已达到目标数量上限，扫描停止") + "\n")
	}
	return builder.String()
}

// renderEntries 渲染本机出口探测结果
func (m *Model) renderEntries() string {
	if m.settings.Mode != ModeEntry {
		return "  " + dimStyle.Render("当前为按国家/地区 ASN 模式，不使用本机出口") + "\n"
	}
	if m.probing {
		return "  " + dimStyle.Render("正在探测本机出口 IPv4/IPv6…") + "\n"
	}
	if len(m.entries) == 0 {
		return "  " + warnStyle.Render("尚未探测到出口 IP，按 r 重新探测") + "\n"
	}
	var builder strings.Builder
	for _, option := range m.entries {
		line := "  " + option.Summary()
		if option.Available {
			line = "  " + okStyle.Render("✔") + " " + option.Summary()
		} else {
			line = "  " + warnStyle.Render("!") + " " + option.Summary()
		}
		builder.WriteString(line + "\n")
	}
	return builder.String()
}

// renderResults 渲染命中结果表
func (m *Model) renderResults() string {
	var builder strings.Builder
	builder.WriteString(labelStyle.Render(fmt.Sprintf("命中目标（%d 条，仅在面板内展示，不写入数据库）", len(m.rows))))
	builder.WriteString("\n")
	header := fmt.Sprintf("%s %s %s %s %s %s %s %s %s",
		pad("域名", 30), pad("IP", 16), pad("ASN", 12), pad("国家", 6),
		pad("星级", 5), pad("握手", 8), pad("证书天", 7), pad("CDN", 5), "状态码")
	builder.WriteString(dimStyle.Render(header))
	builder.WriteString("\n")

	if len(m.rows) == 0 {
		builder.WriteString("  " + dimStyle.Render("尚无命中目标") + "\n")
		return builder.String()
	}

	visible := m.visibleResultRows()
	offset := m.clampedOffset(visible)
	for i := offset; i < len(m.rows) && i < offset+visible; i++ {
		row := m.rows[i]
		cursor := "  "
		if m.focus == FocusResults && i == m.resultOffset {
			cursor = cursorMark.Render("› ")
		}
		builder.WriteString(fmt.Sprintf("%s%s %s %s %s %s %s %s %s %d\n",
			cursor,
			pad(row.Domain, 30),
			pad(row.IP, 16),
			pad(emptyAs(row.ASN, "—"), 12),
			pad(emptyAs(row.Country, "—"), 6),
			pad(fmt.Sprintf("%d 星", row.Stars), 5),
			pad(fmt.Sprintf("%d ms", row.HandshakeMS), 8),
			pad(fmt.Sprintf("%d", row.CertDays), 7),
			pad(yesNo(row.IsCDN), 5),
			row.StatusCode))
	}
	if remaining := len(m.rows) - offset - visible; remaining > 0 {
		builder.WriteString("  " + dimStyle.Render(fmt.Sprintf("… 还有 %d 条，按 tab 切到结果区后用 ↑↓ 滚动", remaining)) + "\n")
	}
	return builder.String()
}

// renderFooter 渲染底部状态栏
func (m *Model) renderFooter() string {
	tone := m.statusMsg
	switch m.statusKind {
	case "ok":
		tone = okStyle.Render(tone)
	case "warn":
		tone = warnStyle.Render(tone)
	case "err":
		tone = errStyle.Render(tone)
	}
	line := "状态：" + tone
	if m.settingsNote != "" {
		line += "\n" + dimStyle.Render(m.settingsNote)
	}
	return line
}

// visibleResultRows 返回结果区可显示行数
func (m *Model) visibleResultRows() int {
	height := m.height
	if height <= 0 {
		height = 34
	}
	visible := height - 20
	if visible < 3 {
		visible = 3
	}
	return visible
}

// clampedOffset 把结果区滚动偏移限制在合法范围内
func (m *Model) clampedOffset(visible int) int {
	maxOffset := len(m.rows) - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.resultOffset > maxOffset {
		m.resultOffset = maxOffset
	}
	if m.resultOffset < 0 {
		m.resultOffset = 0
	}
	return m.resultOffset
}

// pad 按中英混排显示宽度补齐空格
func pad(value string, width int) string {
	current := 0
	for _, r := range value {
		if r < 128 {
			current++
		} else {
			current += 2
		}
	}
	if current >= width {
		return value
	}
	return value + strings.Repeat(" ", width-current)
}

func emptyAs(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// humanInt 千分位整数
func humanInt(value int64) string {
	text := fmt.Sprintf("%d", value)
	if len(text) <= 3 {
		return text
	}
	var out []byte
	for i, r := range []byte(text) {
		if i > 0 && (len(text)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, r)
	}
	return string(out)
}

// humanRate 速率显示
func humanRate(value float64) string {
	if value <= 0 {
		return "0 IP/s"
	}
	if value >= 1000 {
		return fmt.Sprintf("%.1fk IP/s", value/1000)
	}
	return fmt.Sprintf("%.0f IP/s", value)
}

// humanDuration 耗时显示
func humanDuration(value time.Duration) string {
	if value <= 0 {
		return "0s"
	}
	value = value.Round(time.Second)
	if value < time.Minute {
		return fmt.Sprintf("%ds", int(value.Seconds()))
	}
	if value < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(value.Minutes()), int(value.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(value.Hours()), int(value.Minutes())%60)
}
