// Package remotedesk ToDesk远程桌面管理模块
//
// 负责：
// 1. Windows启动时运行ToDesk.exe（不阻塞）
// 2. 定期执行ToDeskSunDump.exe -a 获取设备代码和临时密码
// 3. 将获取到的信息提供给上报模块
package remotedesk

import (
	"context"
	"errors"
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
	mu               sync.Mutex
	deviceCode       string // 设备代码
	tempPassword     string // 临时密码
	running          int32
	done             chan struct{}
	refreshInterval  int // 定期刷新间隔（秒）
	toDeskDir        string
	dumpExe          string
	lastStartAttempt time.Time // 上次尝试启动 ToDesk 的时间
}

const (
	// toDeskFetchAttempts 单次刷新内的最大尝试次数（内存读取存在偶发失败）
	toDeskFetchAttempts = 3
	// toDeskRestartWindow 自动拉起 ToDesk 的最小间隔
	toDeskRestartWindow = 10 * time.Minute
)

// errNoOutput 工具无输出
var errNoOutput = errors.New("ToDeskSunDump.exe无输出")

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

// startToDesk 启动ToDesk.exe（不阻塞主进程），启动后隐藏主窗口
func (m *ToDeskManager) startToDesk() {
	m.mu.Lock()
	m.lastStartAttempt = time.Now()
	m.mu.Unlock()

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
//
// 内存读取存在偶发失败，单次失败不再直接丢弃：最多重试 3 次，
// 设备代码与临时密码分别更新（只拿到一部分时保留另一部分的旧值）。
func (m *ToDeskManager) fetchToDeskInfo() {
	if _, err := os.Stat(m.dumpExe); err != nil {
		logger.Warnf("[ToDesk] ToDeskSunDump.exe不存在: %s", m.dumpExe)
		return
	}

	var (
		lastOutput  string
		foundCode   string
		foundPass   string
		processGone bool
	)

	for attempt := 1; attempt <= toDeskFetchAttempts; attempt++ {
		output, err := m.dumpOnce()
		if err != nil {
			lastOutput = output
			if attempt < toDeskFetchAttempts {
				time.Sleep(time.Second)
			}
			continue
		}

		code, pass := parseToDeskOutput(output)
		if code != "" {
			foundCode = code
		}
		if pass != "" {
			foundPass = pass
		}
		if strings.Contains(output, "未找到ToDesk进程") {
			processGone = true
		}

		lastOutput = output
		if foundCode != "" && foundPass != "" {
			break
		}
		if attempt < toDeskFetchAttempts {
			time.Sleep(time.Second)
		}
	}

	// ToDesk 进程不在时尝试拉起（避免内存读取永远失败）
	if processGone {
		m.restartToDeskIfNeeded()
	}

	if foundCode == "" && foundPass == "" {
		logger.Warnf("[ToDesk] 未能从输出中解析到设备代码或临时密码")
		logger.Debugf("[ToDesk] 原始输出:\n%s", lastOutput)
		return
	}

	m.applyToDeskInfo(foundCode, foundPass)
	if foundCode == "" || foundPass == "" {
		logger.Debugf("[ToDesk] 本次仅获取到部分信息（设备代码=%q 临时密码=%q）", foundCode, foundPass)
	}
}

// dumpOnce 执行一次 ToDeskSunDump.exe，返回解码后的输出
func (m *ToDeskManager) dumpOnce() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, m.dumpExe, "-a")
	cmd.Dir = filepath.Dir(m.dumpExe)

	raw, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			logger.Errorf("[ToDesk] ToDeskSunDump.exe执行超时")
			return "", err
		}

		var exitErr *exec.ExitError
		if ok := asExitError(err, &exitErr); ok {
			logger.Warnf("[ToDesk] ToDeskSunDump.exe执行失败，返回码: %d", exitErr.ExitCode())
			return "", err
		}

		logger.Errorf("[ToDesk] 执行ToDeskSunDump.exe异常: %v", err)
		return "", err
	}

	output := decodeOutput(raw)
	if output == "" {
		logger.Warnf("[ToDesk] ToDeskSunDump.exe无输出")
		return "", errNoOutput
	}
	return output, nil
}

// parseToDeskOutput 从输出中解析设备代码和临时密码
func parseToDeskOutput(output string) (string, string) {
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

	return deviceCode, tempPassword
}

// applyToDeskInfo 更新设备代码与临时密码（分别更新，允许只更新一部分）
func (m *ToDeskManager) applyToDeskInfo(deviceCode, tempPassword string) {
	m.mu.Lock()
	if deviceCode != "" {
		m.deviceCode = deviceCode
	}
	if tempPassword != "" {
		m.tempPassword = tempPassword
	}
	currentCode, currentPass := m.deviceCode, m.tempPassword
	m.mu.Unlock()

	logger.Infof("[ToDesk] 设备代码: %s | 临时密码: %s", currentCode, currentPass)
}

// restartToDeskIfNeeded 在检测到 ToDesk 进程缺失时尝试重新启动（限频）
func (m *ToDeskManager) restartToDeskIfNeeded() {
	m.mu.Lock()
	if time.Since(m.lastStartAttempt) < toDeskRestartWindow {
		m.mu.Unlock()
		return
	}
	m.lastStartAttempt = time.Now()
	m.mu.Unlock()

	logger.Warnf("[ToDesk] 检测到ToDesk进程未运行，尝试重新启动")
	m.startToDesk()
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
