// Package logger 复刻原 loguru 日志行为：
//   - 控制台输出：`时间 | 级别(左对齐7位) | 消息`，级别由配置控制
//   - 文件输出：logs/cloudinit_YYYY-MM-DD.log，固定 DEBUG 级别，按天轮转，按保留期清理
package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Level 日志级别
type Level int

// 日志级别（与 loguru 数值一致）
const (
	DebugLevel    Level = 10
	InfoLevel     Level = 20
	WarningLevel  Level = 30
	ErrorLevel    Level = 40
	CriticalLevel Level = 50
)

const logDir = "logs"

var (
	mu sync.Mutex
	// 与 loguru 默认处理器一致：初始化前输出所有级别
	consoleLevel = DebugLevel
	retention    = 7 * 24 * time.Hour
	file         *os.File
	currentDate  string
)

// Init 初始化日志输出，level 为控制台级别，retention 为文件保留期（如 "7 days"）
func Init(level string, retentionStr string) {
	mu.Lock()
	defer mu.Unlock()

	consoleLevel = ParseLevel(level)
	retention = ParseRetention(retentionStr)

	_ = os.MkdirAll(logDir, 0o755)
	ensureFile(time.Now())
}

// ParseLevel 解析日志级别，无法识别时回退为 INFO
func ParseLevel(level string) Level {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		return DebugLevel
	case "INFO":
		return InfoLevel
	case "WARNING":
		return WarningLevel
	case "ERROR":
		return ErrorLevel
	case "CRITICAL":
		return CriticalLevel
	default:
		return InfoLevel
	}
}

// ParseRetention 解析保留期字符串（支持 s/m/h/d/w 单位），默认 7 天
func ParseRetention(s string) time.Duration {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" {
		return 7 * 24 * time.Hour
	}

	// 提取数字前缀，其余部分作为单位（支持 "7 days" / "24h" / "7" ）
	i := 0
	for i < len(v) && v[i] >= '0' && v[i] <= '9' {
		i++
	}
	if i == 0 {
		return 7 * 24 * time.Hour
	}

	n, err := strconv.Atoi(v[:i])
	if err != nil {
		return 7 * 24 * time.Hour
	}
	unit := strings.TrimSpace(v[i:])
	if unit == "" {
		// 纯数字按秒处理
		return time.Duration(n) * time.Second
	}

	switch {
	case strings.HasPrefix(unit, "s"):
		return time.Duration(n) * time.Second
	case strings.HasPrefix(unit, "m"):
		return time.Duration(n) * time.Minute
	case strings.HasPrefix(unit, "h"):
		return time.Duration(n) * time.Hour
	case strings.HasPrefix(unit, "d"):
		return time.Duration(n) * 24 * time.Hour
	case strings.HasPrefix(unit, "w"):
		return time.Duration(n) * 7 * 24 * time.Hour
	default:
		return time.Duration(n) * 24 * time.Hour
	}
}

func ensureFile(now time.Time) {
	date := now.Format("2006-01-02")
	if file != nil && currentDate == date {
		return
	}

	if file != nil {
		_ = file.Close()
		file = nil
	}

	cleanup(now)

	path := filepath.Join(logDir, "cloudinit_"+date+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	file = f
	currentDate = date
}

// cleanup 删除超过保留期的历史日志
func cleanup(now time.Time) {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "cloudinit_") || !strings.HasSuffix(name, ".log") {
			continue
		}
		dateStr := strings.TrimSuffix(strings.TrimPrefix(name, "cloudinit_"), ".log")
		date, err2 := time.ParseInLocation("2006-01-02", dateStr, time.Local)
		if err2 != nil {
			continue
		}
		if now.Sub(date) > retention {
			_ = os.Remove(filepath.Join(logDir, name))
		}
	}
}

func levelName(l Level) string {
	switch l {
	case DebugLevel:
		return "DEBUG"
	case InfoLevel:
		return "INFO"
	case WarningLevel:
		return "WARNING"
	case ErrorLevel:
		return "ERROR"
	case CriticalLevel:
		return "CRITICAL"
	default:
		return "INFO"
	}
}

func logf(level Level, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	now := time.Now()

	mu.Lock()
	defer mu.Unlock()

	if level >= consoleLevel {
		fmt.Fprintf(os.Stdout, "%s | %-7s | %s\n",
			now.Format("2006-01-02 15:04:05"), levelName(level), msg)
	}

	ensureFile(now)
	if file != nil {
		_, _ = fmt.Fprintf(file, "%s | %-7s | %s\n",
			now.Format("2006-01-02 15:04:05"), levelName(level), msg)
	}
}

// Debugf DEBUG 级别日志
func Debugf(format string, args ...interface{}) { logf(DebugLevel, format, args...) }

// Infof INFO 级别日志
func Infof(format string, args ...interface{}) { logf(InfoLevel, format, args...) }

// Warnf WARNING 级别日志
func Warnf(format string, args ...interface{}) { logf(WarningLevel, format, args...) }

// Errorf ERROR 级别日志
func Errorf(format string, args ...interface{}) { logf(ErrorLevel, format, args...) }

// Criticalf CRITICAL 级别日志
func Criticalf(format string, args ...interface{}) { logf(CriticalLevel, format, args...) }
