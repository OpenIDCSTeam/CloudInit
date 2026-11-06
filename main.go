// CloudInit 服务入口
// OpenIDC 虚拟机初始化与状态上报服务
//
// 功能：
// - 定时采集虚拟机硬件状态并上报至宿主机
// - 接收宿主机下发的控制指令（关机/重启/改密/改主机名）
// - 后台自动磁盘扩容
// - 后台自动版本更新（24h周期）
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"CloudInit/config"
	"CloudInit/core"
	"CloudInit/logger"
	"CloudInit/nic"
	"CloudInit/platform"
	"CloudInit/remotedesk"
	"CloudInit/status"
)

const zeroMAC = "00:00:00:00:00:00"

// statusPayload 上报数据（硬件状态 + 附加信息）
type statusPayload struct {
	*status.HWStatus
	RdpInfo *remotedesk.RDPInfo `json:"rdp_info,omitempty"`
}

// CloudInitService CloudInit 主服务
type CloudInitService struct {
	running int32

	platform    platform.Platform
	vmStatus    *status.VMStatus
	vmManage    *core.VMManage
	powerCtrl   *core.PowerCtrl
	diskExtend  *core.DiskExtend
	autoUpdate  *core.AutoUpdate
	cmdExecutor *core.CmdExecutor
	rdpManager  *remotedesk.Manager

	// 网络增量计算缓存
	lastNetworkU int
	lastNetworkD int
	lastFluUsage int

	// 各子网上次成功上报的主控地址（子网前缀 -> IP），用于后续优先命中
	masterTargets map[string]string

	client *http.Client
}

// NewCloudInitService 创建主服务
func NewCloudInitService() *CloudInitService {
	p, err := platform.New()
	if err != nil {
		logger.Criticalf("平台初始化失败: %v", err)
		os.Exit(1)
	}

	return &CloudInitService{
		platform:      p,
		vmStatus:      status.NewVMStatus(),
		vmManage:      core.NewVMManage(p),
		powerCtrl:     core.NewPowerCtrl(p),
		diskExtend:    core.NewDiskExtend(p),
		autoUpdate:    core.NewAutoUpdate(),
		cmdExecutor:   core.NewCmdExecutor(),
		rdpManager:    remotedesk.New(),
		masterTargets: map[string]string{},
		client:        &http.Client{},
	}
}

// Start 启动服务主流程
func (s *CloudInitService) Start() {
	atomic.StoreInt32(&s.running, 1)
	s.registerSignals()

	line := strings.Repeat("=", 50)
	logger.Infof("%s", line)
	logger.Infof("CloudInit 服务启动")
	logger.Infof("平台: %s | 上报间隔: %ds | 端口: %d",
		platformName(), config.Cfg.ReportInterval, config.Cfg.ReportPort)
	if config.Cfg.ReportHost != "" {
		logger.Infof("上报地址: %s（固定配置）", config.Cfg.ReportHost)
	} else {
		logger.Infof("上报地址: 自动推算（网关偏移: +%d）", config.Cfg.GatewayOffset)
	}
	logger.Infof("%s", line)

	// 启动后台任务（非阻塞，失败不影响主流程）
	safeRun("自动更新启动失败（不影响主服务）", func() {
		if config.Cfg.UpdateEnabled {
			s.autoUpdate.Start()
		} else {
			logger.Infof("[自动更新] 已通过配置禁用")
		}
	})

	safeRun("磁盘扩容启动失败（不影响主服务）", func() {
		s.diskExtend.Extend()
	})

	// 启动ToDesk远程桌面管理（仅Windows）
	safeRun("ToDesk管理启动失败（不影响主服务）", func() {
		s.rdpManager.Start()
	})

	// 非VMware环境下卸载VMware Tools（仅Windows平台）
	safeRun("VMTools检测启动失败（不影响主服务）", func() {
		if handler, ok := s.platform.(platform.VMToolsHandler); ok {
			handler.UninstallVMToolsIfNotVMware()
		}
	})

	// 异步激活Windows（仅Windows平台）
	safeRun("Windows激活启动失败（不影响主服务）", func() {
		if activator, ok := s.platform.(platform.WindowsActivator); ok {
			activator.ActivateWindows()
		}
	})

	// 异步检测 KVM 平台并安装 virtio 驱动（仅Windows平台）
	safeRun("virtio驱动安装启动失败（不影响主服务）", func() {
		if installer, ok := s.platform.(platform.VirtioDriverInstaller); ok {
			installer.InstallVirtioDrivers()
		}
	})

	// 进入主循环
	s.mainLoop()
}

// Stop 优雅停止服务
func (s *CloudInitService) Stop() {
	atomic.StoreInt32(&s.running, 0)

	safeRun("", func() { s.autoUpdate.Stop() })
	safeRun("", func() { s.rdpManager.Stop() })

	logger.Infof("CloudInit 服务已停止")
}

// registerSignals 注册系统信号，支持优雅退出
func (s *CloudInitService) registerSignals() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-signals
		signum := 0
		if sysSig, ok := sig.(syscall.Signal); ok {
			signum = int(sysSig)
		}
		logger.Infof("收到退出信号(%d), 正在停止服务...", signum)
		s.Stop()
	}()
}

// mainLoop 主循环 - 定时采集并上报虚拟机状态
func (s *CloudInitService) mainLoop() {
	var timeLast time.Time

	// 初始化网卡管理器
	nicManager, err := nic.New()
	if err != nil {
		logger.Criticalf("网卡管理器初始化失败: %v", err)
		return
	}

	for atomic.LoadInt32(&s.running) == 1 {
		time.Sleep(time.Second)
		now := time.Now()

		if now.Sub(timeLast) < time.Duration(config.Cfg.ReportInterval)*time.Second {
			continue
		}

		// 采集硬件状态（失败不中断循环）
		if !s.collectStatus() {
			timeLast = now
			continue
		}

		statusData := &statusPayload{HWStatus: s.vmStatus.ToDict()}

		// 附加ToDesk远程桌面信息
		if rdpInfo := s.rdpManager.GetRDPInfo(); rdpInfo != nil {
			statusData.RdpInfo = rdpInfo
		}

		// 确定上报目标并发送
		if config.Cfg.ReportHost != "" {
			// 使用固定配置的上报地址
			// 宿主机接口要求 nic 参数为合法 MAC，否则直接返回 400
			url := fmt.Sprintf("http://%s:%d%s",
				config.Cfg.ReportHost, config.Cfg.ReportPort, config.Cfg.ReportPath)
			if mac := firstValidMAC(nicManager); mac != "" {
				url += "?nic=" + mac
			}
			s.reportStatus(url, statusData, 5*time.Second, false)
		} else {
			// 自动从网卡网关推算上报地址：在子网内遍历，直到找到主控
			// 同一子网只探测一次，避免多网卡重复扫描
			resolved := map[string]string{}
			for _, nicConfig := range nicManager.NICList {
				if nicConfig.MacAddr == zeroMAC {
					continue
				}
				if nicConfig.Ip4Gate == "" || !strings.HasSuffix(nicConfig.Ip4Gate, ".1") {
					continue
				}

				prefix := subnetPrefix(nicConfig.Ip4Gate)
				if prefix == "" {
					continue
				}

				targetIP, ok := resolved[prefix]
				if !ok {
					// 首次遇到该子网：遍历候选地址并顺带完成本网卡上报
					targetIP = s.reportViaNIC(nicConfig, prefix, statusData)
					resolved[prefix] = targetIP
					continue
				}

				// 同一子网的其它网卡：直接上报到已找到的主控
				if targetIP != "" {
					url := buildReportURL(targetIP, nicConfig.MacAddr)
					s.reportStatus(url, statusData, 5*time.Second, true)
				}
			}
		}

		timeLast = now
	}
}

// reportViaNIC 在网卡所在子网内遍历候选地址，直到找到主控并完成上报
// 返回找到的主控IP，未找到时返回空字符串
func (s *CloudInitService) reportViaNIC(nicConfig *nic.NCConfig, prefix string, data *statusPayload) string {
	candidates := masterCandidates(prefix, config.Cfg.GatewayOffset)

	// 优先使用上次成功的主控地址，避免每轮都全网扫描
	if lastTarget := s.masterTargets[prefix]; lastTarget != "" {
		candidates = moveToFront(candidates, lastTarget)
	}

	for index, targetIP := range candidates {
		// 首个候选（缓存/默认偏移）给足超时；扫描其余地址时用短超时，避免长时间阻塞
		timeout := time.Second
		if index == 0 {
			timeout = 5 * time.Second
		}

		url := buildReportURL(targetIP, nicConfig.MacAddr)
		if s.reportStatus(url, data, timeout, true) {
			if s.masterTargets[prefix] != targetIP {
				logger.Infof("[上报] 子网 %s* 找到主控: %s", prefix, targetIP)
				s.masterTargets[prefix] = targetIP
			}
			return targetIP
		}
	}

	delete(s.masterTargets, prefix)
	logger.Warnf("[上报] 网卡 %s 所在子网 %s* 未找到主控，本轮不上报",
		nicConfig.MacAddr, prefix)
	return ""
}

// buildReportURL 构造上报地址
func buildReportURL(targetIP, mac string) string {
	return fmt.Sprintf("http://%s:%d%s?nic=%s",
		targetIP, config.Cfg.ReportPort, config.Cfg.ReportPath, mac)
}

// firstValidMAC 返回第一个有效网卡 MAC（宿主机上报接口强校验该参数）
func firstValidMAC(nicManager *nic.NCManage) string {
	for _, nicConfig := range nicManager.NICList {
		if nicConfig.MacAddr == "" || nicConfig.MacAddr == zeroMAC {
			continue
		}
		return nicConfig.MacAddr
	}
	return ""
}

// subnetPrefix 提取网关所在子网前缀，如 192.168.1.
func subnetPrefix(gateway string) string {
	parts := strings.Split(gateway, ".")
	if len(parts) != 4 {
		return ""
	}
	return parts[0] + "." + parts[1] + "." + parts[2] + "."
}

// masterCandidates 生成子网内的候选地址（从偏移量开始递增，末尾回绕）
func masterCandidates(prefix string, offset int) []string {
	start := offset
	if start < 1 {
		start = 1 // offset=0 表示直接上报到网关
	}
	if start > 254 {
		start = 254
	}

	candidates := make([]string, 0, 254)
	for last := start; last <= 254; last++ {
		candidates = append(candidates, prefix+strconv.Itoa(last))
	}
	// 回绕，补齐 start 之前的地址（跳过网络地址 .0 与网关 .1）
	for last := 2; last < start; last++ {
		candidates = append(candidates, prefix+strconv.Itoa(last))
	}
	return candidates
}

// moveToFront 将指定地址移动到候选列表首位（不存在时保持原顺序）
func moveToFront(candidates []string, value string) []string {
	for index, item := range candidates {
		if item != value {
			continue
		}
		result := make([]string, 0, len(candidates))
		result = append(result, value)
		result = append(result, candidates[:index]...)
		result = append(result, candidates[index+1:]...)
		return result
	}
	return candidates
}

// collectStatus 采集状态并计算网络增量
func (s *CloudInitService) collectStatus() (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("状态采集异常: %v", r)
			ok = false
		}
	}()

	s.vmStatus.Status()
	s.calcNetworkDelta()
	return true
}

// calcNetworkDelta 计算网络增量数据（本周期相对上周期的变化量）
func (s *CloudInitService) calcNetworkDelta() {
	currentU := s.vmStatus.VM.NetworkU
	currentD := s.vmStatus.VM.NetworkD
	currentFlu := s.vmStatus.VM.FluUsage

	s.vmStatus.VM.NetworkU = currentU - s.lastNetworkU
	s.vmStatus.VM.NetworkD = currentD - s.lastNetworkD
	s.vmStatus.VM.FluUsage = currentFlu - s.lastFluUsage

	s.lastNetworkU = currentU
	s.lastNetworkD = currentD
	s.lastFluUsage = currentFlu
}

// reportStatus 上报状态到宿主机，并处理返回的控制指令
// 返回 true 表示该地址上有主控应答（HTTP 200 且响应可解析）
func (s *CloudInitService) reportStatus(url string, data *statusPayload, timeout time.Duration, quiet bool) bool {
	body, err := json.Marshal(data)
	if err != nil {
		logger.Errorf("上报异常: %v", err)
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		logger.Errorf("上报异常: %v", err)
		return false
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		if quiet {
			logger.Debugf("上报失败: %s (%v)", url, err)
		} else if ctx.Err() == context.DeadlineExceeded {
			logger.Debugf("上报超时: %s", url)
		} else if isConnectionError(err) {
			logger.Debugf("上报连接失败: %s", url)
		} else {
			logger.Errorf("上报异常: %v", err)
		}
		return false
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Errorf("上报异常: %v", err)
		return false
	}

	var response struct {
		Code int                    `json:"code"`
		Msg  string                 `json:"msg"`
		Data map[string]interface{} `json:"data"`
	}
	if err = json.Unmarshal(raw, &response); err != nil {
		// 不是主控的协议响应，继续扫描其它地址
		if quiet {
			logger.Debugf("上报响应非主控格式: %s (%v)", url, err)
		} else {
			logger.Errorf("上报异常: %v", err)
		}
		return false
	}

	if resp.StatusCode != 200 {
		// 主控已应答（协议匹配），不再继续扫描；错误信息对排查很关键
		logger.Warnf("[上报] 主控返回 HTTP %d: %s", resp.StatusCode, response.Msg)
		return true
	}

	vmData := response.Data
	if len(vmData) == 0 {
		return true
	}

	// 更新虚拟机配置信息
	s.vmManage.SetConfig("vm_uuid", mapString(vmData, "vm_uuid"))
	s.vmManage.SetConfig("vm_pass", mapString(vmData, "vm_pass"))

	// 处理控制指令
	vmFlag := mapString(vmData, "vm_flag")
	if vmFlag == "ON_STOP" || vmFlag == "S_CLOSE" {
		logger.Warnf("收到关机指令, 执行关机...")
		s.powerCtrl.ShutdownSystem()
		atomic.StoreInt32(&s.running, 0)
		return true
	}
	if vmFlag == "S_RESET" {
		logger.Warnf("收到重启指令, 执行重启...")
		s.powerCtrl.RebootSystem()
		atomic.StoreInt32(&s.running, 0)
		return true
	}

	// 非阻塞执行虚拟机配置（改主机名、改密码）
	s.vmManage.Manage()

	// 处理远程命令下发（兼容服务端无此字段的情况）
	if vmCmd, ok := vmData["vm_cmd"].(map[string]interface{}); ok {
		if mapString(vmCmd, "command") != "" {
			// 构建回传基础URL（去掉路径部分）
			baseURL := url
			if index := strings.Index(url, "/api/"); index >= 0 {
				baseURL = url[:index]
			} else if index = strings.LastIndex(url, "/"); index >= 0 {
				baseURL = url[:index]
			}
			s.cmdExecutor.ExecuteCommand(vmCmd, baseURL)
		}
	}

	return true
}

// safeRun 执行可能失败的任务，异常仅记录日志
func safeRun(errorMessage string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			if errorMessage != "" {
				logger.Errorf("%s: %v", errorMessage, r)
			}
		}
	}()

	fn()
}

// platformName 返回平台实现名称（对应 Python 的类型名）
func platformName() string {
	switch runtime.GOOS {
	case "windows":
		return "PlatformWindows"
	case "darwin":
		return "PlatformMacOS"
	default:
		return "PlatformLinux"
	}
}

// mapString 读取 map 中的字符串字段
func mapString(data map[string]interface{}, key string) string {
	value, ok := data[key]
	if !ok || value == nil {
		return ""
	}
	if str, okStr := value.(string); okStr {
		return str
	}
	return fmt.Sprint(value)
}

// isConnectionError 判断是否为连接类错误
func isConnectionError(err error) bool {
	type timeoutError interface {
		Timeout() bool
	}
	if netErr, ok := err.(timeoutError); ok && netErr.Timeout() {
		return false
	}
	return true
}

// ==================== 入口 ====================
func main() {
	logger.Init(config.Cfg.LogLevel, config.Cfg.LogRetention)

	// 老版本工具（MASAIO.cmd / ToDesk 组件）从程序根目录迁移到 tool 子目录
	core.MigrateTools()

	service := NewCloudInitService()
	service.Start()
}
