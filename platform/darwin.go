package platform

import (
	"strings"

	"CloudInit/logger"
)

type darwinPlatform struct {
	base
}

func newDarwin() *darwinPlatform {
	return &darwinPlatform{base: base{hostsPath: "/etc/hosts"}}
}

// ==================== 电源控制 ====================

func (p *darwinPlatform) Shutdown() {
	logger.Infof("[系统关机] 执行 macOS 关机命令")
	if res := runCmd("shutdown", "-h", "now"); res.Err != nil || res.Returncode != 0 {
		logger.Errorf("[系统关机] 关机命令执行失败: %s", cmdErrText(res))
	}
}

func (p *darwinPlatform) Reboot() {
	logger.Infof("[系统重启] 执行 macOS 重启命令")
	if res := runCmd("shutdown", "-r", "now"); res.Err != nil || res.Returncode != 0 {
		logger.Errorf("[系统重启] 重启命令执行失败: %s", cmdErrText(res))
	}
}

// ==================== 主机配置 ====================

func (p *darwinPlatform) GetHostname() string {
	result := runCmd("scutil", "--get", "ComputerName")
	if result.Returncode == 0 && result.Err == nil {
		return trimStdout(result)
	}
	// 备用
	return trimStdout(runCmd("hostname"))
}

func (p *darwinPlatform) SetHostname(hostname string) {
	current := p.GetHostname()
	if current == hostname {
		logger.Infof("[macOS主机名] 当前主机名已经是: %s，无需修改", hostname)
		return
	}

	logger.Infof("[macOS主机名] 当前主机名: %s，需要修改为: %s", current, hostname)

	success := true
	// 设置 ComputerName
	if result := runCmd("scutil", "--set", "ComputerName", hostname); result.Err != nil || result.Returncode != 0 {
		logger.Errorf("[macOS主机名] 设置ComputerName失败: %s", cmdErrText(result))
		success = false
	}

	// 设置 HostName
	if result := runCmd("scutil", "--set", "HostName", hostname); result.Err != nil || result.Returncode != 0 {
		logger.Errorf("[macOS主机名] 设置HostName失败: %s", cmdErrText(result))
		success = false
	}

	// 设置 LocalHostName（去掉特殊字符）
	localHostname := strings.ReplaceAll(strings.ReplaceAll(hostname, " ", "-"), ".", "-")
	if result := runCmd("scutil", "--set", "LocalHostName", localHostname); result.Err != nil || result.Returncode != 0 {
		logger.Errorf("[macOS主机名] 设置LocalHostName失败: %s", cmdErrText(result))
		success = false
	}

	if success {
		logger.Infof("[macOS主机名] 主机名设置成功: %s", hostname)
	} else {
		logger.Warnf("[macOS主机名] 部分主机名设置失败")
	}

	// 更新hosts
	p.updateHosts(hostname)
}

func (p *darwinPlatform) SetPassword(username, password string) {
	logger.Infof("[macOS密码] 设置%s密码", username)
	result := runCmd("dscl", ".", "-passwd", "/Users/"+username, password)
	if result.Err != nil {
		logger.Errorf("[macOS密码] %s密码设置失败: %v", username, result.Err)
		return
	}
	if result.Returncode == 0 {
		logger.Infof("[macOS密码] %s密码设置成功", username)
	} else {
		logger.Errorf("[macOS密码] %s密码设置失败: %s", username, result.Stderr)
	}
}

// ==================== 磁盘扩容 ====================

func (p *darwinPlatform) ExtendDisk() error {
	logger.Infof("[磁盘扩容] 执行 macOS 磁盘扩容")

	// 获取APFS容器信息
	result := runCmd("diskutil", "apfs", "list")
	if result.Err != nil {
		logger.Errorf("[磁盘扩容] 获取APFS信息失败: %v", result.Err)
		return nil
	}
	if result.Returncode != 0 {
		logger.Errorf("[磁盘扩容] 获取APFS信息失败: %s", result.Stderr)
		return nil
	}

	// 尝试对根卷执行resize
	resize := runCmd("diskutil", "apfs", "resizeContainer", "disk1", "0")
	if resize.Err != nil {
		logger.Errorf("[磁盘扩容] macOS 磁盘扩容失败: %v", resize.Err)
		return nil
	}
	if resize.Returncode == 0 {
		logger.Infof("[磁盘扩容] macOS APFS 扩容成功")
		return nil
	}

	logger.Warnf("[磁盘扩容] APFS扩容结果: %s", resize.Stderr)
	// 备用：尝试repairDisk
	repair := runCmd("diskutil", "repairDisk", "disk0")
	if repair.Err != nil {
		logger.Errorf("[磁盘扩容] macOS 磁盘扩容失败: %v", repair.Err)
		return nil
	}
	if repair.Returncode == 0 {
		logger.Infof("[磁盘扩容] diskutil repairDisk 完成")
	} else {
		logger.Errorf("[磁盘扩容] macOS 磁盘扩容失败: %s", repair.Stderr)
	}

	return nil
}
