package logger

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/schollz/progressbar/v3"
)

type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// Logger 统一日志与人类可读信息输出器
//
// 输出目标固定为 stderr（可再镜像到日志文件），stdout 保留给机器可读数据。
// 每行格式：[HH:MM:SS] <图标> <内容>
type Logger struct {
	mu         sync.Mutex
	level      Level
	out        io.Writer
	fileWriter io.WriteCloser
}

var globalLogger *Logger = NewLogger("info", "")

func NewLogger(levelStr string, logFile string) *Logger {
	l := &Logger{
		level: parseLevel(levelStr),
		out:   os.Stderr,
	}
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			l.fileWriter = f
			l.out = io.MultiWriter(os.Stderr, f)
		}
	}
	return l
}

func Init(levelStr string, logFile string) {
	globalLogger = NewLogger(levelStr, logFile)
}

// SetOutput 覆盖人类可读信息的输出目标（测试使用）
func SetOutput(w io.Writer) {
	globalLogger.mu.Lock()
	defer globalLogger.mu.Unlock()
	globalLogger.out = w
}

// writer 返回当前输出目标
func (l *Logger) writer() io.Writer {
	return l.out
}

func parseLevel(l string) Level {
	switch strings.ToLower(l) {
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

func IsDebug() bool {
	return globalLogger.level <= LevelDebug
}

// AboveBar 在打印日志前清掉进度条，打印完毕后让进度条重新出现在下一行
func AboveBar(bar *progressbar.ProgressBar, format string, a ...any) {
	if bar != nil {
		_ = bar.Clear()
	}
	fmt.Fprintf(globalLogger.writer(), format, a...)
	if bar != nil {
		_ = bar.RenderBlank()
	}
}

func Debug(format string, args ...interface{}) {
	if globalLogger.level <= LevelDebug {
		logMsg(IconDetail, format, args...)
	}
}

func Info(format string, args ...interface{}) {
	if globalLogger.level <= LevelInfo {
		logMsg(IconStep, format, args...)
	}
}

func Warn(format string, args ...interface{}) {
	if globalLogger.level <= LevelWarn {
		logMsg(IconWarn, format, args...)
	}
}

func Error(format string, args ...interface{}) {
	if globalLogger.level <= LevelError {
		logMsg(IconError, format, args...)
	}
}

func logMsg(icon string, format string, args ...interface{}) {
	globalLogger.mu.Lock()
	defer globalLogger.mu.Unlock()
	timestamp := time.Now().Format("15:04:05")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(globalLogger.out, "[%s] %s %s\n", timestamp, icon, msg)
}

// PrintTimestampedMessage 打印一行信息（等价于 Info，保留兼容旧调用点）
func PrintTimestampedMessage(format string, args ...interface{}) {
	Info(format, args...)
}

// PrintError 打印一行错误信息
func PrintError(message string) {
	Error("%s", message)
}

// PrintErrorWithDetails 打印错误信息与随后的细节说明行
func PrintErrorWithDetails(message string, details ...string) {
	Error("%s", message)
	for _, detail := range details {
		Detail("%s", detail)
	}
}
