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

// rdpInfo 远程桌面信息
type rdpInfo struct {
	ToDesk *remotedesk.ToDeskInfo `json:"todesk"`
}

// statusPayload 上报数据（硬件状态 + 附加信息）
type statusPayload struct {
	*status.HWStatus
	RdpInfo *rdpInfo `json:"rdp_info,omitempty"`
}

// CloudInitService CloudInit 主服务
type CloudInitService struct {
	running int32

	platform      platform.Platform
	vmStatus      *status.VMStatus
	vmManage      *core.VMManage
	powerCtrl     *core.PowerCtrl
	diskExtend    *core.DiskExtend
	autoUpdate    *core.AutoUpdate
	cmdExecutor   *core.CmdExecutor
	toDeskManager *remotedesk.ToDeskManager

	// 网络增量计算缓存
	lastNetworkU int
	lastNetworkD int
	lastFluUsage int

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
		toDeskManager: remotedesk.New(),
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
		s.toDeskManager.Start()
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

	// 进入主循环
	s.mainLoop()
}

// Stop 优雅停止服务
func (s *CloudInitService) Stop() {
	atomic.StoreInt32(&s.running, 0)

	safeRun("", func() { s.autoUpdate.Stop() })
	safeRun("", func() { s.toDeskManager.Stop() })

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
		if toDeskInfo := s.toDeskManager.GetToDeskInfo(); toDeskInfo != nil {
			statusData.RdpInfo = &rdpInfo{ToDesk: toDeskInfo}
		}

		// 确定上报目标并发送
		if config.Cfg.ReportHost != "" {
			// 使用固定配置的上报地址
			url := fmt.Sprintf("http://%s:%d%s",
				config.Cfg.ReportHost, config.Cfg.ReportPort, config.Cfg.ReportPath)
			s.reportStatus(url, statusData)
		} else {
			// 自动从网卡网关推算上报地址
			for _, nicConfig := range nicManager.NICList {
				gateway := nicConfig.Ip4Gate
				if gateway == "" || !strings.HasSuffix(gateway, ".1") {
					continue
				}
				if nicConfig.MacAddr == zeroMAC {
					continue
				}

				// 根据网关偏移量计算目标IP
				parts := strings.Split(gateway, ".")
				parts[len(parts)-1] = strconv.Itoa(config.Cfg.GatewayOffset)
				targetIP := strings.Join(parts, ".")

				url := fmt.Sprintf("http://%s:%d%s?nic=%s",
					targetIP, config.Cfg.ReportPort, config.Cfg.ReportPath, nicConfig.MacAddr)
				s.reportStatus(url, statusData)
			}
		}

		timeLast = now
	}
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
func (s *CloudInitService) reportStatus(url string, data *statusPayload) {
	body, err := json.Marshal(data)
	if err != nil {
		logger.Errorf("上报异常: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		logger.Errorf("上报异常: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			logger.Debugf("上报超时: %s", url)
		} else if isConnectionError(err) {
			logger.Debugf("上报连接失败: %s", url)
		} else {
			logger.Errorf("上报异常: %v", err)
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		logger.Warnf("上报失败 [%s] HTTP %d", url, resp.StatusCode)
		return
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Errorf("上报异常: %v", err)
		return
	}

	var response struct {
		Data map[string]interface{} `json:"data"`
	}
	if err = json.Unmarshal(raw, &response); err != nil {
		logger.Errorf("上报异常: %v", err)
		return
	}

	vmData := response.Data
	if len(vmData) == 0 {
		return
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
		return
	}
	if vmFlag == "S_RESET" {
		logger.Warnf("收到重启指令, 执行重启...")
		s.powerCtrl.RebootSystem()
		atomic.StoreInt32(&s.running, 0)
		return
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

	service := NewCloudInitService()
	service.Start()
}
