package platform

import (
	"bytes"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"CloudInit/logger"
)

type linuxPlatform struct {
	base
}

func newLinux() *linuxPlatform {
	return &linuxPlatform{base: base{hostsPath: "/etc/hosts"}}
}

// ==================== 电源控制 ====================

func (p *linuxPlatform) Shutdown() {
	logger.Infof("[系统关机] 执行 Linux 关机命令")
	if res := runCmd("shutdown", "-h", "now"); res.Err != nil || res.Returncode != 0 {
		logger.Errorf("[系统关机] 关机命令执行失败: %s", cmdErrText(res))
	}
}

func (p *linuxPlatform) Reboot() {
	logger.Infof("[系统重启] 执行 Linux 重启命令")
	if res := runCmd("reboot"); res.Err != nil || res.Returncode != 0 {
		logger.Errorf("[系统重启] 重启命令执行失败: %s", cmdErrText(res))
	}
}

// cmdErrText 提取命令失败原因（启动错误优先，其次使用标准错误）
func cmdErrText(res cmdResult) string {
	if res.Err != nil {
		return res.Err.Error()
	}
	return res.Stderr
}

// ==================== 主机配置 ====================

func (p *linuxPlatform) GetHostname() string {
	return trimStdout(runCmd("hostname"))
}

func (p *linuxPlatform) SetHostname(hostname string) {
	current := p.GetHostname()
	if current == hostname {
		logger.Infof("[Linux主机名] 当前主机名已经是: %s，无需修改", hostname)
		return
	}

	logger.Infof("[Linux主机名] 当前主机名: %s，需要修改为: %s", current, hostname)
	result := runCmd("hostnamectl", "set-hostname", hostname)
	if result.Err != nil {
		// 与原逻辑一致：命令无法启动视为异常，直接放弃本次主机名设置
		logger.Errorf("[Linux主机名] 设置失败: %v", result.Err)
		return
	}
	if result.Returncode == 0 {
		logger.Infof("[Linux主机名] 主机名设置成功: %s", hostname)
	} else {
		// 备用方式：直接写文件
		if err := os.WriteFile("/etc/hostname", []byte(hostname+"\n"), 0o644); err != nil {
			logger.Errorf("[Linux主机名] 设置失败: %v", err)
		} else if fallback := runCmd("hostname", hostname); fallback.Err != nil || fallback.Returncode != 0 {
			msg := fallback.Stderr
			if msg == "" && fallback.Err != nil {
				msg = fallback.Err.Error()
			}
			logger.Errorf("[Linux主机名] 设置失败: %s", msg)
		} else {
			logger.Infof("[Linux主机名] 传统方式设置成功: %s", hostname)
		}
	}

	// 更新hosts
	p.updateHosts(hostname)
}

func (p *linuxPlatform) SetPassword(username, password string) {
	logger.Infof("[Linux密码] 设置%s密码", username)

	cmd := exec.Command("chpasswd")
	var stdout, stderr bytes.Buffer
	cmd.Stdin = strings.NewReader(username + ":" + password)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		logger.Errorf("[Linux密码] %s密码设置失败: %s", username, sanitizeText(stderr.String()))
		return
	}
	logger.Infof("[Linux密码] %s密码设置成功", username)
}

// ==================== 磁盘扩容 ====================

func (p *linuxPlatform) ExtendDisk() error {
	logger.Infof("[磁盘扩容] 执行 Linux 磁盘扩容")

	rootDevice := p.getRootPartition()
	if rootDevice == "" {
		logger.Errorf("[磁盘扩容] 无法检测根分区设备")
		return nil
	}

	logger.Infof("[磁盘扩容] 检测到根分区: %s", rootDevice)

	// 解析磁盘和分区号
	disk, partNum, ok := parsePartition(rootDevice)
	if !ok {
		logger.Errorf("[磁盘扩容] 无法解析分区信息: %s", rootDevice)
		return nil
	}

	logger.Infof("[磁盘扩容] 磁盘: %s, 分区号: %d", disk, partNum)

	// 执行 growpart 扩展分区
	result := runCmd("growpart", disk, strconv.Itoa(partNum))
	if result.Err != nil {
		return result.Err
	}
	if result.Returncode == 0 {
		logger.Infof("[磁盘扩容] growpart 扩展分区成功")
	} else if strings.Contains(result.Stdout, "NOCHANGE") || strings.Contains(result.Stderr, "NOCHANGE") {
		logger.Infof("[磁盘扩容] 分区已是最大，无需扩展")
	} else {
		logger.Warnf("[磁盘扩容] growpart 执行结果: %s %s", result.Stdout, result.Stderr)
	}

	// 检测文件系统类型并执行对应的扩容命令
	fsType := p.getFsType(rootDevice)
	logger.Infof("[磁盘扩容] 文件系统类型: %s", fsType)

	switch fsType {
	case "ext2", "ext3", "ext4":
		resize := runCmd("resize2fs", rootDevice)
		if resize.Err != nil {
			return resize.Err
		}
		if resize.Returncode == 0 {
			logger.Infof("[磁盘扩容] resize2fs 扩容成功")
		} else {
			logger.Errorf("[磁盘扩容] resize2fs 失败: %s", resize.Stderr)
		}
	case "xfs":
		resize := runCmd("xfs_growfs", "/")
		if resize.Err != nil {
			return resize.Err
		}
		if resize.Returncode == 0 {
			logger.Infof("[磁盘扩容] xfs_growfs 扩容成功")
		} else {
			logger.Errorf("[磁盘扩容] xfs_growfs 失败: %s", resize.Stderr)
		}
	case "btrfs":
		resize := runCmd("btrfs", "filesystem", "resize", "max", "/")
		if resize.Err != nil {
			return resize.Err
		}
		if resize.Returncode == 0 {
			logger.Infof("[磁盘扩容] btrfs 扩容成功")
		} else {
			logger.Errorf("[磁盘扩容] btrfs 扩容失败: %s", resize.Stderr)
		}
	default:
		logger.Warnf("[磁盘扩容] 不支持的文件系统类型: %s", fsType)
	}

	return nil
}

// ==================== 内部方法 ====================

// getRootPartition 获取根分区设备路径
func (p *linuxPlatform) getRootPartition() string {
	result := runCmd("findmnt", "-n", "-o", "SOURCE", "/")
	if result.Err != nil {
		logger.Errorf("[磁盘扩容] 获取根分区失败: %v", result.Err)
		return ""
	}
	if result.Returncode == 0 {
		source := trimStdout(result)
		if source != "" {
			if strings.Contains(source, "/mapper/") {
				logger.Infof("[磁盘扩容] 检测到LVM设备: %s，尝试扩展LV", source)
				runCmd("lvextend", "-l", "+100%FREE", source)
			}
			return source
		}
	}

	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		logger.Errorf("[磁盘扩容] 获取根分区失败: %v", err)
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[1] == "/" {
			return parts[0]
		}
	}

	return ""
}

var (
	nvmePartRe = regexp.MustCompile(`^(/dev/nvme\d+n\d+)p(\d+)$`)
	diskPartRe = regexp.MustCompile(`^(/dev/[a-z]+)(\d+)$`)
)

// parsePartition 解析分区设备为磁盘路径和分区号
func parsePartition(device string) (string, int, bool) {
	// NVMe设备: /dev/nvme0n1p2
	if m := nvmePartRe.FindStringSubmatch(device); m != nil {
		num, err := strconv.Atoi(m[2])
		if err != nil {
			return "", 0, false
		}
		return m[1], num, true
	}
	// 普通设备: /dev/sda2, /dev/vda1
	if m := diskPartRe.FindStringSubmatch(device); m != nil {
		num, err := strconv.Atoi(m[2])
		if err != nil {
			return "", 0, false
		}
		return m[1], num, true
	}
	return "", 0, false
}

// getFsType 获取设备的文件系统类型
func (p *linuxPlatform) getFsType(device string) string {
	result := runCmd("blkid", "-s", "TYPE", "-o", "value", device)
	if result.Err != nil {
		logger.Errorf("[磁盘扩容] 获取文件系统类型失败: %v", result.Err)
		return ""
	}
	if result.Returncode == 0 {
		return trimStdout(result)
	}
	return ""
}
