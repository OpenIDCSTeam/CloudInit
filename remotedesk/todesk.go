// Package remotedesk ToDesk远程桌面管理模块
//
// 负责：
// 1. Windows启动时运行ToDesk.exe（不阻塞）
// 2. 定期执行ToDeskSunDump.exe -a 获取设备代码和临时密码
// 3. 将获取到的信息提供给上报模块
package remotedesk

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"

	"CloudInit/apppath"
	"CloudInit/logger"
)

var (
	deviceCodeRe   = regexp.MustCompile(`^设备代码[:：]\s*(.+)`)
	tempPasswordRe = regexp.MustCompile(`^临时密码[:：]\s*(.+)`)
)

// ToDeskInfo ToDesk 连接信息
type ToDeskInfo struct {
	Code     string `json:"code"`
	Password string `json:"password"`
}

// ToDeskManager ToDesk远程桌面管理器
type ToDeskManager struct {
	mu              sync.Mutex
	deviceCode      string // 设备代码
	tempPassword    string // 临时密码
	running         int32
	done            chan struct{}
	refreshInterval int // 定期刷新间隔（秒）
	toDeskDir       string
	dumpExe         string
}

// New 创建 ToDesk 管理器
func New() *ToDeskManager {
	toolDir := apppath.ToolDir()
	return &ToDeskManager{
		refreshInterval: 120, // 每2分钟刷新一次
		toDeskDir:       toolDir,
		dumpExe:         filepath.Join(toolDir, "ToDeskSunDump.exe"),
	}
}

// Start 启动ToDesk管理（仅Windows平台）
func (m *ToDeskManager) Start() {
	if runtime.GOOS != "windows" {
		logger.Infof("[ToDesk] 非Windows平台，跳过ToDesk管理")
		return
	}

	// 启动ToDesk.exe（不阻塞）
	m.startToDesk()

	// 启动定期刷新协程
	atomic.StoreInt32(&m.running, 1)
	m.done = make(chan struct{})
	go m.refreshLoop()

	logger.Infof("[ToDesk] ToDesk管理已启动，刷新间隔: %ds", m.refreshInterval)
}

// Stop 停止ToDesk管理
func (m *ToDeskManager) Stop() {
	atomic.StoreInt32(&m.running, 0)
	if m.done == nil {
		return
	}
	select {
	case <-m.done:
	case <-time.After(5 * time.Second):
	}
}

// GetToDeskInfo 获取ToDesk连接信息，用于上报
func (m *ToDeskManager) GetToDeskInfo() *ToDeskInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.deviceCode != "" && m.tempPassword != "" {
		return &ToDeskInfo{
			Code:     m.deviceCode,
			Password: m.tempPassword,
		}
	}
	return nil
}

// startToDesk 启动ToDesk.exe（通过start命令启动，不阻塞主进程），启动后隐藏主窗口
func (m *ToDeskManager) startToDesk() {
	toDeskExe := filepath.Join(m.toDeskDir, "ToDesk.exe")
	if _, err := os.Stat(toDeskExe); err != nil {
		logger.Warnf("[ToDesk] ToDesk.exe不存在: %s", toDeskExe)
		return
	}

	// 独立启动ToDesk.exe，确保其正常运行且不阻塞主进程
	cmd := exec.Command(toDeskExe)
	cmd.Dir = m.toDeskDir
	if err := cmd.Start(); err != nil {
		logger.Errorf("[ToDesk] 启动ToDesk.exe失败: %v", err)
		return
	}

	logger.Infof("[ToDesk] ToDesk.exe已通过start启动: %s", toDeskExe)

	// 启动后台协程延迟隐藏窗口
	go hideToDeskWindow()
}

// refreshLoop 定期刷新ToDesk设备代码和临时密码
func (m *ToDeskManager) refreshLoop() {
	defer close(m.done)

	// 首次启动等待10秒让ToDesk完全启动
	time.Sleep(10 * time.Second)

	for atomic.LoadInt32(&m.running) == 1 {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Errorf("[ToDesk] 获取ToDesk信息异常: %v", r)
				}
			}()
			m.fetchToDeskInfo()
		}()

		// 等待下一次刷新
		for i := 0; i < m.refreshInterval; i++ {
			if atomic.LoadInt32(&m.running) == 0 {
				return
			}
			time.Sleep(time.Second)
		}
	}
}

// fetchToDeskInfo 执行ToDeskSunDump.exe -a 获取设备代码和临时密码
func (m *ToDeskManager) fetchToDeskInfo() {
	if _, err := os.Stat(m.dumpExe); err != nil {
		logger.Warnf("[ToDesk] ToDeskSunDump.exe不存在: %s", m.dumpExe)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, m.dumpExe, "-a")
	cmd.Dir = filepath.Dir(m.dumpExe)

	raw, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			logger.Errorf("[ToDesk] ToDeskSunDump.exe执行超时")
			return
		}

		var exitErr *exec.ExitError
		if ok := asExitError(err, &exitErr); ok {
			logger.Warnf("[ToDesk] ToDeskSunDump.exe执行失败，返回码: %d", exitErr.ExitCode())
			return
		}

		logger.Errorf("[ToDesk] 执行ToDeskSunDump.exe异常: %v", err)
		return
	}

	output := decodeOutput(raw)
	if output == "" {
		logger.Warnf("[ToDesk] ToDeskSunDump.exe无输出")
		return
	}

	// 解析输出，提取设备代码和临时密码
	deviceCode := ""
	tempPassword := ""

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if match := deviceCodeRe.FindStringSubmatch(line); match != nil {
			deviceCode = strings.TrimSpace(match[1])
			continue
		}
		if match := tempPasswordRe.FindStringSubmatch(line); match != nil {
			tempPassword = strings.TrimSpace(match[1])
			continue
		}
	}

	if deviceCode != "" && tempPassword != "" {
		m.mu.Lock()
		m.deviceCode = deviceCode
		m.tempPassword = tempPassword
		m.mu.Unlock()
		logger.Infof("[ToDesk] 设备代码: %s | 临时密码: %s", deviceCode, tempPassword)
	} else {
		logger.Warnf("[ToDesk] 未能从输出中解析到设备代码或临时密码")
		logger.Debugf("[ToDesk] 原始输出:\n%s", output)
	}
}

// decodeOutput 优先UTF-8，失败则尝试GBK，均失败则替换错误字符
func decodeOutput(raw []byte) string {
	if utf8.Valid(raw) {
		return string(raw)
	}

	if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(raw); err == nil {
		return string(decoded)
	}

	return strings.ToValidUTF8(string(raw), "\uFFFD")
}

// asExitError 包装 errors.As，避免在调用处引入额外导入
func asExitError(err error, target **exec.ExitError) bool {
	exitErr, ok := err.(*exec.ExitError)
	if ok {
		*target = exitErr
	}
	return ok
}
