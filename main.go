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

	// activePort 当前可用的主控端口（1881 不通时自动切到 1880）
	activePort int32

	// done 主循环退出信号（服务模式下 Stop 后等待退出）
	done chan struct{}

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
		platform:    p,
		vmStatus:    status.NewVMStatus(),
		vmManage:    core.NewVMManage(p),
		powerCtrl:   core.NewPowerCtrl(p),
		diskExtend:  core.NewDiskExtend(p),
		autoUpdate:  core.NewAutoUpdate(),
		cmdExecutor: core.NewCmdExecutor(),
		rdpManager:  remotedesk.New(),
		done:        make(chan struct{}),
		client:      newReportClient(),
	}
}

// newReportClient 构造上报专用客户端
// 上报走内网直连，明确禁用系统代理（HTTP_PROXY/HTTPS_PROXY），
// 否则探测子网地址时可能被代理劫持，误判为找到主控。
func newReportClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{Proxy: nil},
		Timeout:   0, // 由每次请求的 context 控制超时
	}
}

// Start 启动服务主流程
func (s *CloudInitService) Start() {
	defer close(s.done)

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

	// 磁盘扩容、VMware Tools 卸载都可能耗时数分钟（msiexec/diskpart），
	// 必须放到后台执行，否则会卡住主循环导致「不上报」
	go safeRun("磁盘扩容异常（不影响主服务）", func() {
		if !config.Cfg.DiskExtendEnabled {
			logger.Infof("[磁盘扩容] 已在配置中禁用")
			return
		}
		s.diskExtend.Extend()
	})

	// 启动ToDesk远程桌面管理（仅Windows）
	safeRun("ToDesk管理启动失败（不影响主服务）", func() {
		s.rdpManager.Start()
	})

	// 非VMware环境下卸载VMware Tools（仅Windows平台）
	go safeRun("VMTools检测异常（不影响主服务）", func() {
		if handler, ok := s.platform.(platform.VMToolsHandler); ok {
			handler.UninstallVMToolsIfNotVMware()
		}
	})

	// 异步激活Windows（仅Windows平台）
	safeRun("Windows激活启动失败（不影响主服务）", func() {
		if !config.Cfg.ActivationEnabled {
			logger.Infof("[Windows激活] 已在配置中禁用")
			return
		}
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

	if !config.Cfg.ReportEnabled {
		logger.Infof("[上报] 已在配置中禁用状态上报，仅保持服务运行")
		for atomic.LoadInt32(&s.running) == 1 {
			time.Sleep(time.Second)
		}
		return
	}

	logger.Infof("[上报] 主循环已启动，间隔 %ds，主控端口 %d（兜底 %d），网卡数 %d",
		config.Cfg.ReportInterval, config.Cfg.ReportPort, config.Cfg.ReportFallbackPort, len(nicManager.NICList))
	for _, nicConfig := range nicManager.NICList {
		logger.Infof("[上报] 网卡 %s | IP: %s | 网关: %s",
			nicConfig.MacAddr, nicConfig.Ip4Addr, nicConfig.Ip4Gate)
	}
	if config.Cfg.ReportHost != "" {
		logger.Infof("[上报] 目标主控: %s（固定配置）", config.Cfg.ReportHost)
	} else {
		logger.Infof("[上报] 目标主控: 网关偏移 +%d", config.Cfg.GatewayOffset)
	}
	if config.Cfg.ScanSubnet {
		logger.Warnf("[上报] scan_subnet 已开启：固定地址不可达时会扫描整个网段，内网主机可能冒充主控，请仅在可信环境使用")
	} else {
		logger.Infof("[上报] 网段扫描已关闭（默认）")
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

		// 向配置的主控上报一次（不扫描网段）
		s.reportOnce(nicManager, statusData)

		timeLast = now
	}
}

// reportOnce 向配置的主控上报一次状态
//
// 只与「配置的地址」或「网关推算出的固定地址」通信，不扫描网段：
// 避免内网任意主机监听同端口就冒充主控、下发改密指令。
func (s *CloudInitService) reportOnce(nicManager *nic.NCManage, data *statusPayload) {
	mac := firstValidMAC(nicManager)
	if mac == "" {
		logger.Debugf("[上报] 没有可用网卡 MAC，跳过本轮")
		return
	}

	host := strings.TrimSpace(config.Cfg.ReportHost)
	if host == "" {
		host = gatewayMaster(nicManager)
		if host == "" {
			logger.Warnf("[上报] 未配置 report_host 且无法从网关推算主控地址，本轮不上报")
			return
		}
	}

	port := atomic.LoadInt32(&s.activePort)
	if port <= 0 {
		port = int32(config.Cfg.ReportPort)
	}

	if s.reportTo(host, int(port), mac, data) {
		return
	}

	// 端口兜底：1881 → 1880（可通过配置关闭）
	fallback := config.Cfg.ReportFallbackPort
	if fallback <= 0 || fallback == int(port) {
		logger.Warnf("[上报] 主控 %s:%d 上报失败", host, port)
		return
	}

	if s.reportTo(host, fallback, mac, data) {
		atomic.StoreInt32(&s.activePort, int32(fallback))
		logger.Infof("[上报] 主控端口已切换为 %d", fallback)
		return
	}

	// 网段扫描默认关闭：开启后内网任意主机都可能被当主控，存在被改密的风险
	if config.Cfg.ScanSubnet {
		if found := s.scanSubnet(nicManager, mac, data); found != "" {
			logger.Warnf("[上报] 通过网段扫描找到主控 %s（scan_subnet 已开启）", found)
			return
		}
	}

	logger.Warnf("[上报] 主控 %s:%d 与兜底端口 %d 均不可达", host, port, fallback)
}

// scanSubnet 顺序扫描网关所在 /24 网段（仅 scan_subnet=true 时启用）
func (s *CloudInitService) scanSubnet(nicManager *nic.NCManage, mac string, data *statusPayload) string {
	prefix := firstGatewayPrefix(nicManager)
	if prefix == "" {
		return ""
	}

	port := int(atomic.LoadInt32(&s.activePort))
	if port <= 0 {
		port = config.Cfg.ReportPort
	}

	for last := 1; last <= 254; last++ {
		ip := prefix + strconv.Itoa(last)
		url := fmt.Sprintf("http://%s:%d%s?nic=%s", ip, port, config.Cfg.ReportPath, mac)

		// 探测阶段不处理响应，命中后再正式上报一次
		if !s.reportStatus(url, data, 600*time.Millisecond, true, false) {
			continue
		}
		if s.reportStatus(url, data, 5*time.Second, false, true) {
			return ip
		}
	}

	return ""
}

// firstGatewayPrefix 返回第一块有网关网卡的 /24 前缀
func firstGatewayPrefix(nicManager *nic.NCManage) string {
	for _, nicConfig := range nicManager.NICList {
		if nicConfig.MacAddr == zeroMAC || !isIPv4Literal(nicConfig.Ip4Gate) {
			continue
		}
		if prefix := subnetPrefix(nicConfig.Ip4Gate); prefix != "" {
			return prefix
		}
	}
	return ""
}

// reportTo 上报到指定主控
func (s *CloudInitService) reportTo(host string, port int, mac string, data *statusPayload) bool {
	url := fmt.Sprintf("http://%s:%d%s?nic=%s", host, port, config.Cfg.ReportPath, mac)
	if !s.reportStatus(url, data, 5*time.Second, false, true) {
		return false
	}

	logger.Infof("[上报] 状态已上报（主控 %s:%d）", host, port)
	return true
}

// gatewayMaster 由网关推算主控地址（网关前三段 + gateway_offset）
func gatewayMaster(nicManager *nic.NCManage) string {
	for _, nicConfig := range nicManager.NICList {
		if nicConfig.MacAddr == zeroMAC || !isIPv4Literal(nicConfig.Ip4Gate) {
			continue
		}
		prefix := subnetPrefix(nicConfig.Ip4Gate)
		if prefix == "" {
			continue
		}
		return prefix + strconv.Itoa(config.Cfg.GatewayOffset)
	}
	return ""
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

// isIPv4Literal 判断是否为合法的点分十进制 IPv4
func isIPv4Literal(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 3 {
			return false
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return false
			}
		}
	}
	return true
}

// subnetPrefix 提取网关所在子网前缀，如 192.168.1.
func subnetPrefix(gateway string) string {
	parts := strings.Split(gateway, ".")
	if len(parts) != 4 {
		return ""
	}
	return parts[0] + "." + parts[1] + "." + parts[2] + "."
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
// handle=false 时只探测不处理响应，用于并发扫描阶段
func (s *CloudInitService) reportStatus(url string, data *statusPayload, timeout time.Duration, quiet, handle bool) bool {
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
			logger.Warnf("上报超时: %s", url)
		} else if isConnectionError(err) {
			logger.Warnf("上报连接失败: %s", url)
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
		Code      int                    `json:"code"`
		Msg       string                 `json:"msg"`
		Data      map[string]interface{} `json:"data"`
		Timestamp int64                  `json:"timestamp"`
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

	// 主控响应固定带 timestamp（Python/Go 两版都如此）：
	// 借此区分防火墙/透明代理返回的普通 JSON，避免误判为主控
	if response.Code == 0 || response.Timestamp == 0 {
		if quiet {
			logger.Debugf("上报响应缺少主控标识: %s", url)
		} else {
			logger.Warnf("上报响应不是主控协议（HTTP %d）: %s", resp.StatusCode, url)
		}
		return false
	}

	if resp.StatusCode != 200 {
		// 主控已应答（协议匹配），不再继续扫描；错误信息对排查很关键
		logger.Warnf("[上报] 主控返回 HTTP %d: %s", resp.StatusCode, response.Msg)
		return true
	}

	if !handle {
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

	// Windows 服务（SCM 启动）时交给服务框架托管，其余平台前台运行
	if runAsService(service) {
		return
	}

	service.Start()
}
