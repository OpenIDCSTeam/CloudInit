package platform

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"CloudInit/apppath"
	"CloudInit/logger"
)

type windowsPlatform struct {
	base
}

func newWindows() *windowsPlatform {
	return &windowsPlatform{base: base{hostsPath: `C:\Windows\System32\drivers\etc\hosts`}}
}

// ==================== 电源控制 ====================

func (p *windowsPlatform) Shutdown() {
	logger.Infof("[系统关机] 执行 Windows 关机命令")
	if res := runCmd("shutdown", "/s", "/t", "0", "/f"); res.Err != nil || res.Returncode != 0 {
		logger.Errorf("[系统关机] 关机命令执行失败: %s", cmdErrText(res))
	}
}

func (p *windowsPlatform) Reboot() {
	logger.Infof("[系统重启] 执行 Windows 重启命令")
	if res := runCmd("shutdown", "/r", "/t", "0", "/f"); res.Err != nil || res.Returncode != 0 {
		logger.Errorf("[系统重启] 重启命令执行失败: %s", cmdErrText(res))
	}
}

// ==================== 主机配置 ====================

func (p *windowsPlatform) GetHostname() string {
	return trimStdout(runShell("hostname"))
}

func (p *windowsPlatform) SetHostname(hostname string) {
	current := p.GetHostname()
	if strings.EqualFold(current, hostname) {
		logger.Infof("[Windows主机名] 当前主机名已经是: %s，无需修改", hostname)
		return
	}

	logger.Infof("[Windows主机名] 当前主机名: %s，需要修改为: %s", current, hostname)

	// 方案1: 使用 netdom (兼容 Windows 7)
	netdomCmd := fmt.Sprintf(`netdom renamecomputer %%computername%% /newname:%s /force`, hostname)
	result := runShell(netdomCmd)

	if result.Err == nil && result.Returncode == 0 {
		logger.Infof("[Windows主机名] netdom 设置成功，需要重启后生效: %s", hostname)
	} else {
		// 方案2: PowerShell Rename-Computer (Windows 8+)
		logger.Warnf("[Windows主机名] netdom 设置失败: %s，尝试 Rename-Computer", cmdErrText(result))
		powershellCmd := fmt.Sprintf(`powershell -Command "Rename-Computer -NewName \"%s\" -Force"`, hostname)
		fallback := runShell(powershellCmd)
		if fallback.Err == nil && fallback.Returncode == 0 {
			logger.Infof("[Windows主机名] Rename-Computer 设置成功，需要重启后生效: %s", hostname)
		} else {
			logger.Errorf("[Windows主机名] 两种方式均设置失败: %s", cmdErrText(fallback))
		}
	}

	// 更新hosts
	p.updateHosts(hostname)
}

func (p *windowsPlatform) SetPassword(username, password string) {
	// Windows只需要设置Administrator的密码
	username = "Administrator"

	logger.Infof("[Windows密码] 设置%s密码", username)

	// 先检查用户是否存在
	if check := runShell("net user " + username); check.Returncode != 0 || check.Err != nil {
		logger.Warnf("[Windows密码] 用户%s不存在，跳过密码设置", username)
		return
	}

	// 使用shell执行，密码用引号包裹防止特殊字符被解析
	result := runShell(fmt.Sprintf(`net user %s "%s"`, username, password))
	if result.Returncode == 0 && result.Err == nil {
		logger.Infof("[Windows密码] %s密码设置成功", username)
	} else {
		logger.Errorf("[Windows密码] %s密码设置失败: %s", username, cmdErrText(result))
	}
}

// ==================== 磁盘扩容 ====================

func (p *windowsPlatform) ExtendDisk() error {
	logger.Infof("[磁盘扩容] 执行 Windows 磁盘扩容")
	runShell(`mshta vbscript:Execute("CreateObject(""WScript.Shell"").Run ""cmd /c (echo select volume C&&echo extend)|diskpart"",0,True:close")`)
	logger.Infof("[磁盘扩容] Windows 磁盘扩容完成")
	return nil
}

// ==================== VMware Tools管理 ====================

// UninstallVMToolsIfNotVMware 检测虚拟化环境，如果不是VMware/ESXi环境则处理VMware Tools
func (p *windowsPlatform) UninstallVMToolsIfNotVMware() {
	go p.doCheckAndUninstallVMTools()
	logger.Infof("[VMTools管理] 已启动异步虚拟化环境检测线程")
}

func (p *windowsPlatform) doCheckAndUninstallVMTools() {
	// 检测虚拟化环境（多种方式综合判断）
	isVMware := false

	// 方法1：通过wmic查询硬件信息（Win7/Win10兼容）
	combinedInfo := ""
	wmicCmds := []string{
		"wmic bios get serialnumber,manufacturer /format:list",
		"wmic computersystem get manufacturer,model /format:list",
		"wmic baseboard get manufacturer,product /format:list",
	}
	for _, cmdText := range wmicCmds {
		result := runShell(cmdText)
		if result.Returncode == 0 && result.Err == nil {
			combinedInfo += strings.ToLower(result.Stdout) + "\n"
		}
	}

	// 方法2：wmic不可用时（Win11），用PowerShell查询
	if strings.TrimSpace(combinedInfo) == "" {
		psCmd := `powershell -NoProfile -Command "` +
			`(Get-CimInstance Win32_BIOS | Select-Object Manufacturer,SerialNumber | Format-List);` +
			`(Get-CimInstance Win32_ComputerSystem | Select-Object Manufacturer,Model | Format-List);` +
			`(Get-CimInstance Win32_BaseBoard | Select-Object Manufacturer,Product | Format-List)` +
			`"`
		result := runShell(psCmd)
		if result.Returncode == 0 && result.Err == nil {
			combinedInfo = strings.ToLower(result.Stdout)
		}
	}

	if strings.Contains(combinedInfo, "vmware") || strings.Contains(combinedInfo, "esxi") {
		isVMware = true
	}

	// 方法3：通过systeminfo检测（兜底）
	if !isVMware {
		result := runShell("systeminfo")
		if result.Returncode == 0 && result.Err == nil {
			sysinfo := strings.ToLower(result.Stdout)
			if strings.Contains(sysinfo, "vmware") || strings.Contains(sysinfo, "esxi") {
				isVMware = true
			}
		}
	}

	if isVMware {
		logger.Infof("[VMTools管理] 检测到VMware/ESXi虚拟化环境，保留VMware Tools")
		return
	}

	logger.Infof("[VMTools管理] 非VMware/ESXi环境，检查是否安装了VMware Tools")

	// 检查VMware Tools是否已安装（通过注册表查询卸载信息）
	check := runShell(`reg query "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall" /s /f "VMware Tools"`)
	if check.Returncode != 0 || check.Err != nil || !strings.Contains(check.Stdout, "VMware Tools") {
		logger.Infof("[VMTools管理] 未检测到VMware Tools安装，无需卸载")
		return
	}

	logger.Infof("[VMTools管理] 检测到VMware Tools已安装，开始静默卸载")

	// 卸载前先强制结束VM相关进程
	vmProcesses := []string{
		"vmtoolsd.exe", "vmwaretray.exe", "vmacthlp.exe",
		"VGAuthService.exe", "vm3dservice.exe", "vmwareuser.exe",
	}
	for _, proc := range vmProcesses {
		runShell("taskkill /F /IM " + proc)
	}

	// 停止VM相关服务
	vmServices := []string{"VMTools", "VGAuthService", "vm3dservice", "VMwarePhysicalDiskHelper", "VMUSBArbService"}
	for _, svc := range vmServices {
		runShell("net stop " + svc)
	}

	logger.Infof("[VMTools管理] 已结束VM相关进程和服务")
}

// ==================== Windows激活 ====================

// ActivateWindows 异步激活Windows系统
// Win10/11使用HWID方式，Win7/8等旧版本使用TSforge方式
func (p *windowsPlatform) ActivateWindows() {
	go p.doActivateWindows()
	logger.Infof("[Windows激活] 已启动异步激活线程")
}

func (p *windowsPlatform) doActivateWindows() {
	// 获取MASAIO.cmd脚本路径（与CloudInit.exe同目录）
	masaioPath := apppath.BaseDir() + string(os.PathSeparator) + "MASAIO.cmd"
	if _, err := os.Stat(masaioPath); err != nil {
		logger.Warnf("[Windows激活] 激活脚本不存在: %s", masaioPath)
		return
	}

	// 检查是否已激活
	result := runShell(`cscript //nologo C:\Windows\System32\slmgr.vbs /xpr`)
	if result.Returncode == 0 && result.Err == nil {
		if strings.Contains(result.Stdout, "永久激活") ||
			strings.Contains(strings.ToLower(result.Stdout), "permanently activated") {
			logger.Infof("[Windows激活] 系统已永久激活，跳过")
			return
		}
	}

	// 根据Windows版本选择激活方式
	winVer := WindowsVersion()
	majorVer := 0
	if winVer != "" {
		if parts := strings.Split(winVer, "."); len(parts) > 0 {
			if v, err := strconv.Atoi(parts[0]); err == nil {
				majorVer = v
			}
		}
	}

	activateParam := "/Z-Windows /S"
	methodName := "TSforge"
	if majorVer >= 10 {
		// Win10/11 使用 HWID 方式
		activateParam = "/HWID /S"
		methodName = "HWID"
	}

	logger.Infof("[Windows激活] 使用 %s 方式激活 (版本: %s)", methodName, winVer)

	// 执行激活脚本
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	cmdText := fmt.Sprintf(`cmd.exe /c "%s" %s`, masaioPath, activateParam)
	result, err := runShellContext(ctx, cmdText)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			logger.Warnf("[Windows激活] 激活脚本执行超时")
			return
		}
		logger.Warnf("[Windows激活] %s 激活返回码: %d", methodName, result.Returncode)
		if result.Stderr != "" {
			logger.Debugf("[Windows激活] 错误输出: %s", truncateText(result.Stderr, 500))
		}
		return
	}

	logger.Infof("[Windows激活] %s 激活执行完成", methodName)
}
