package logger

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// 统一的 CLI 人类可读输出层。
//
// 约定（详见 src/doc/dev/cli-output-style.md）：
//   - 人类可读信息一律经本文件输出到 stderr，stdout 只留给机器可读数据
//     （pipe 流数据、--json、asn 的 CIDR 列表等），保证 `cmd > file` 结果纯净；
//   - 单行结构固定为 `<图标> <短语>：<关键值>`，每行最多一个时间戳前缀；
//   - 图标只用几何字形，不用 emoji（避免中英混排下终端宽度错位）。
const (
	IconOK     = "✔" // 成功 / 完成
	IconStep   = "›" // 进行中 / 下一步
	IconWarn   = "!" // 警告
	IconError  = "✖" // 错误 / 失败
	IconDetail = "·" // 细节 / 数据行
)

var consoleMu sync.Mutex

// Writer 返回人类可读信息的输出目标（供 flag 等标准库复用同一通道）
func Writer() io.Writer {
	return out().writer()
}

// Line 输出一行标准信息：<图标> <短语>：<关键值>
func Line(icon, label, value string) {
	writeConsole(fmt.Sprintf("%s %s：%s", icon, label, value))
}

// Linef 输出一行标准信息，关键值支持格式化
func Linef(icon, label, format string, args ...interface{}) {
	Line(icon, label, fmt.Sprintf(format, args...))
}

// Detail 输出细节行（两空格缩进 + 细节图标）
func Detail(format string, args ...interface{}) {
	writeConsole(fmt.Sprintf("  %s %s", IconDetail, fmt.Sprintf(format, args...)))
}

// out 返回当前人类可读信息的输出目标
func out() *Logger {
	return globalLogger
}

// Blank 输出一个空行，用于分节留白
func Blank() {
	consoleMu.Lock()
	defer consoleMu.Unlock()
	fmt.Fprintln(out().writer(), "")
}

// Block 输出无时间戳的分节块（用于启动信息、统计大盘等对齐内容）
func Block(lines ...string) {
	consoleMu.Lock()
	defer consoleMu.Unlock()
	w := out().writer()
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
}

// Step 输出一个步骤起始行
func Step(format string, args ...interface{}) {
	writeConsole(fmt.Sprintf("%s %s", IconStep, fmt.Sprintf(format, args...)))
}

// Success 输出一行成功信息
func Success(format string, args ...interface{}) {
	writeConsole(fmt.Sprintf("%s %s", IconOK, fmt.Sprintf(format, args...)))
}

// writeConsole 在时间戳前缀下写入一行人类可读信息
func writeConsole(line string) {
	consoleMu.Lock()
	defer consoleMu.Unlock()
	fmt.Fprintf(out().writer(), "[%s] %s\n", time.Now().Format("15:04:05"), line)
}

// Plain 在无时间戳、无图标的情况下写一行（表格与原始结果）
func Plain(format string, args ...interface{}) {
	consoleMu.Lock()
	defer consoleMu.Unlock()
	fmt.Fprintf(out().writer(), format+"\n", args...)
}

// Interactive 报告当前是否为交互式终端（供颜色/光标控制使用）
func Interactive() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// Alignment 按显示宽度左对齐字符串（中文字符按 2 列计算）
func Alignment(value string, width int) string {
	pad := width - DisplayWidth(value)
	if pad <= 0 {
		return value
	}
	return value + strings.Repeat(" ", pad)
}

// DisplayWidth 计算字符串在等宽终端下的显示宽度
func DisplayWidth(value string) int {
	width := 0
	for _, r := range value {
		if r < 128 {
			width++
		} else {
			width += 2
		}
	}
	return width
}
