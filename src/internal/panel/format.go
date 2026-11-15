package panel

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// 面板与纯文本输出共用的格式化助手（按显示宽度对齐，避免中英混排错位）。

var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

// StripANSI 去掉 ANSI 转义序列（供测试与日志复用）
func StripANSI(text string) string {
	return ansiPattern.ReplaceAllString(text, "")
}

// displayWidth 返回字符串在等宽终端下的显示宽度（CJK 宽字符按 2 列，制表符号按 1 列）
func displayWidth(value string) int {
	width := 0
	for _, r := range StripANSI(value) {
		width += runeWidth(r)
	}
	return width
}

// runeWidth 返回单个字符的显示宽度
func runeWidth(r rune) int {
	if r < 32 || (r >= 0x7f && r < 0xa0) {
		return 0
	}
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
		return 0
	}
	if isWideRune(r) {
		return 2
	}
	return 1
}

// isWideRune 判断是否为 CJK / 全角 / emoji 等占两列的字符。
// 制表符（U+2500 区）与常见符号（✔ ← → 等）宽度为 1，不能按「非 ASCII 即 2」处理，
// 否则横幅与分隔线会与文字行错位。
func isWideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F,
		r >= 0x2E80 && r <= 0x303E,
		r >= 0x3041 && r <= 0x33FF,
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x4E00 && r <= 0x9FFF,
		r >= 0xA000 && r <= 0xA4CF,
		r >= 0xA960 && r <= 0xA97F,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE10 && r <= 0xFE19,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x3FFFD:
		return true
	}
	return false
}

// pad 按显示宽度补齐空格（中英混排、含 ANSI 颜色时同样对齐）
func pad(value string, width int) string {
	current := displayWidth(value)
	if current >= width {
		return value
	}
	return value + strings.Repeat(" ", width-current)
}

// emptyAs 空值回退为占位符
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

// yesNo 返回开关的中文取值
func yesNo(value bool) string {
	if value {
		return "是"
	}
	return "否"
}

// optionalCount 返回「不限 / N 单位」形式的取值
func optionalCount(value int, unit string) string {
	if value <= 0 {
		return "不限"
	}
	return fmt.Sprintf("%d%s", value, unit)
}

// optionalMS 返回「不限 / N ms」形式的取值
func optionalMS(value int64) string {
	if value <= 0 {
		return "不限"
	}
	return fmt.Sprintf("%d ms", value)
}
