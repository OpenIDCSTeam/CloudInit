//go:build windows

package platform

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"CloudInit/logger"
)

// VMware Tools 相关服务名
var vmwareServiceNames = []string{
	"VMTools", "VGAuthService", "vm3dservice",
	"VMwarePhysicalDiskHelper", "VMUSBArbService",
}

// findVMwareToolsProductCode 从卸载注册表读取 VMware Tools 的 MSI ProductCode
func findVMwareToolsProductCode() string {
	roots := []string{
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`,
		`SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`,
	}

	for _, root := range roots {
		key, err := registry.OpenKey(registry.LOCAL_MACHINE, root, registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}
		names, err := key.ReadSubKeyNames(-1)
		_ = key.Close()
		if err != nil {
			continue
		}

		for _, name := range names {
			sub, errSub := registry.OpenKey(registry.LOCAL_MACHINE, root+`\`+name, registry.QUERY_VALUE)
			if errSub != nil {
				continue
			}

			displayName, _, _ := sub.GetStringValue("DisplayName")
			if !strings.Contains(strings.ToLower(displayName), "vmware tools") {
				_ = sub.Close()
				continue
			}

			if code, _, errCode := sub.GetStringValue("ProductCode"); errCode == nil && code != "" {
				_ = sub.Close()
				return code
			}
			if uninstall, _, errUninstall := sub.GetStringValue("UninstallString"); errUninstall == nil {
				if guid := extractProductCode(uninstall); guid != "" {
					_ = sub.Close()
					return guid
				}
			}

			_ = sub.Close()
			// 卸载项子键名通常就是 {ProductCode}
			return name
		}
	}

	return ""
}

// extractProductCode 从卸载命令中提取 {GUID}
func extractProductCode(value string) string {
	start := strings.Index(value, "{")
	if start < 0 {
		return ""
	}
	end := strings.Index(value[start:], "}")
	if end < 0 {
		return ""
	}
	code := value[start : start+end+1]
	if len(code) != 38 {
		return ""
	}
	return code
}

// vmwareToolsInstalled 检测是否安装了 VMware Tools
func vmwareToolsInstalled() bool {
	if findVMwareToolsProductCode() != "" {
		return true
	}

	for _, path := range []string{
		`SOFTWARE\VMware, Inc.\VMware Tools`,
		`SOFTWARE\WOW6432Node\VMware, Inc.\VMware Tools`,
	} {
		if key, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE); err == nil {
			_ = key.Close()
			return true
		}
	}

	return false
}

// stopVMwareServices 停止 VMware 相关服务
//
// 必须先于结束进程执行：进程被强杀后再发送停止控制，SCM 会返回
// 1063(ERROR_FAILED_SERVICE_CONTROLLER_CONNECT)。该错误码在此场景属于正常现象，
// 记录后继续交给卸载程序处理，不能中断卸载流程。
func stopVMwareServices() {
	manager, err := mgr.Connect()
	if err != nil {
		logger.Debugf("[VMTools管理] 连接服务管理器失败: %v", err)
		return
	}
	defer manager.Disconnect()

	for _, name := range vmwareServiceNames {
		service, errOpen := manager.OpenService(name)
		if errOpen != nil {
			if errOpen != windows.ERROR_SERVICE_DOES_NOT_EXIST {
				logger.Debugf("[VMTools管理] 打开服务 %s 失败: %v", name, errOpen)
			}
			continue
		}

		_, errStop := service.Control(svc.Stop)
		switch {
		case errStop == nil:
			if !waitServiceStopped(service, 10*time.Second) {
				logger.Debugf("[VMTools管理] 服务 %s 未在10秒内停止", name)
			}
		case errStop == windows.ERROR_SERVICE_NOT_ACTIVE: // 1062 未运行
		case errStop == windows.ERROR_FAILED_SERVICE_CONTROLLER_CONNECT: // 1063
			logger.Debugf("[VMTools管理] 服务 %s 返回1063（控制器连接失败），继续卸载", name)
		case errStop == windows.ERROR_SERVICE_CANNOT_ACCEPT_CTRL: // 1061 当前状态不接受停止
			logger.Debugf("[VMTools管理] 服务 %s 返回1061（当前状态不接受停止控制）", name)
		default:
			logger.Debugf("[VMTools管理] 停止服务 %s 失败: %v", name, errStop)
		}

		_ = service.Close()
	}
}

// waitServiceStopped 等待服务进入停止状态
func waitServiceStopped(service *mgr.Service, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := service.Query()
		if err != nil {
			return true
		}
		if status.State == svc.Stopped {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// uninstallVMwareTools 执行静默卸载，返回是否成功与最后返回码
func uninstallVMwareTools(productCode string) (bool, int) {
	lastCode := 0

	// 1. MSI 静默卸载
	if productCode != "" {
		lastCode = runShellWithTimeout(
			fmt.Sprintf(`msiexec /x %s /qn /norestart REBOOT=ReallySuppress`, productCode),
			300*time.Second,
		)
		if isUninstallSuccess(lastCode) {
			return true, lastCode
		}
		logger.Warnf("[VMTools管理] msiexec 卸载返回码: %d", lastCode)
	}

	// 2. 官方卸载程序兜底
	for _, path := range []string{
		`C:\Program Files\VMware\VMware Tools\VMwareToolsUninstaller.exe`,
		`C:\Program Files (x86)\VMware\VMware Tools\VMwareToolsUninstaller.exe`,
	} {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		lastCode = runShellWithTimeout(fmt.Sprintf(`"%s" /S`, path), 300*time.Second)
		if isUninstallSuccess(lastCode) {
			return true, lastCode
		}
		logger.Warnf("[VMTools管理] 卸载程序 %s 返回码: %d", path, lastCode)
	}

	// 3. WMI 兜底
	lastCode = runShellWithTimeout(
		`wmic product where "name like 'VMware Tools%'" call uninstall /nointeractive`,
		300*time.Second,
	)
	if !vmwareToolsInstalled() {
		return true, lastCode
	}

	// 4. PowerShell CIM 兜底
	lastCode = runShellWithTimeout(
		`powershell -NoProfile -Command "Get-CimInstance Win32_Product | `+
			`Where-Object {$_.Name -like 'VMware Tools*'} | Invoke-CimMethod -MethodName Uninstall"`,
		300*time.Second,
	)

	return !vmwareToolsInstalled(), lastCode
}

// runShellWithTimeout 以 shell 方式执行命令，返回退出码（超时返回 -1）
func runShellWithTimeout(command string, timeout time.Duration) int {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result, err := runShellContext(ctx, command)
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		logger.Warnf("[VMTools管理] 命令执行超时: %s", command)
		return -1
	}
	return result.Returncode
}

// isUninstallSuccess 判断卸载返回码是否为成功（3010=需重启，1605/1614=已不存在）
func isUninstallSuccess(code int) bool {
	switch code {
	case 0, 3010, 1605, 1614:
		return true
	default:
		return false
	}
}

// removeVMwareServices 删除卸载失败后残留的服务注册项
func removeVMwareServices() {
	manager, err := mgr.Connect()
	if err != nil {
		return
	}
	defer manager.Disconnect()

	for _, name := range vmwareServiceNames {
		service, errOpen := manager.OpenService(name)
		if errOpen != nil {
			continue
		}
		if errDelete := service.Delete(); errDelete == nil {
			logger.Infof("[VMTools管理] 已删除残留服务: %s", name)
		}
		_ = service.Close()
	}
}
