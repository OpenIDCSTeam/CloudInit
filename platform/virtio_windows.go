//go:build windows

package platform

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows/registry"

	"CloudInit/apppath"
	"CloudInit/config"
	"CloudInit/logger"
)

const (
	virtioX64URL = "https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/archive-virtio/virtio-win-0.1.302-1/virtio-win-gt-x64.msi"
	virtioX86URL = "https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/archive-virtio/virtio-win-0.1.302-1/virtio-win-gt-x86.msi"

	virtioPciVendor = "VEN_1AF4" // Red Hat / VirtIO
)

// InstallVirtioDrivers 异步检测虚拟机平台，必要时安装 virtio 驱动
func (p *windowsPlatform) InstallVirtioDrivers() {
	go p.installVirtioDrivers()
}

func (p *windowsPlatform) installVirtioDrivers() {
	if !config.Cfg.VirtioEnabled {
		logger.Debugf("[VirtIO] 已禁用自动检测，跳过")
		return
	}

	vendor := kvmVendor()
	if vendor == "" {
		logger.Debugf("[VirtIO] 非 KVM/QEMU 平台，跳过驱动安装")
		return
	}

	arch := virtioArch()
	if arch == "" {
		logger.Warnf("[VirtIO] 当前架构(%s)无对应驱动包，跳过", runtime.GOARCH)
		return
	}

	logger.Infof("[VirtIO] 检测到 KVM 平台（%s），架构: %s", vendor, arch)

	if virtioGuestToolsInstalled() {
		logger.Infof("[VirtIO] virtio 驱动已安装，跳过")
		return
	}

	found, missing := virtioDriverState()
	if !found {
		logger.Infof("[VirtIO] 未发现 VirtIO 设备，无需安装驱动")
		return
	}
	if !missing {
		logger.Infof("[VirtIO] VirtIO 设备状态正常，无需安装驱动")
		return
	}

	if recentInstallAttempt() {
		logger.Debugf("[VirtIO] 近期安装失败，本次跳过（稍后自动重试）")
		return
	}

	downloadURL := virtioDownloadURL(arch)
	installerPath := filepath.Join(apppath.EnsureToolDir(), "virtio-win-gt-"+arch+".msi")

	if err := ensureVirtioInstaller(downloadURL, installerPath); err != nil {
		logger.Warnf("[VirtIO] 驱动包下载失败: %v", err)
		markInstallAttempt()
		return
	}

	logger.Infof("[VirtIO] 开始静默安装驱动: %s", installerPath)
	if err := installVirtioMSI(installerPath); err != nil {
		logger.Warnf("[VirtIO] 驱动安装失败: %v", err)
		markInstallAttempt()
		return
	}

	logger.Infof("[VirtIO] virtio 驱动安装完成，部分设备可能需重启后生效")
}

// kvmVendor 通过 BIOS 信息判断是否运行在 KVM/QEMU 上
func kvmVendor() string {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`HARDWARE\DESCRIPTION\System\BIOS`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()

	fields := []string{
		"SystemManufacturer", "SystemProductName", "BIOSVendor",
		"BIOSVersion", "BaseBoardManufacturer",
	}
	keywords := []string{"qemu", "kvm", "red hat", "ovirt", "bochs"}

	for _, field := range fields {
		value, _, errValue := key.GetStringValue(field)
		if errValue != nil || value == "" {
			continue
		}
		lower := strings.ToLower(value)
		for _, keyword := range keywords {
			if strings.Contains(lower, keyword) {
				return value
			}
		}
	}

	return ""
}

// virtioArch 返回驱动包对应的架构标识
func virtioArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "x86"
	default:
		return ""
	}
}

// virtioDownloadURL 返回驱动包下载地址（支持配置覆盖）
func virtioDownloadURL(arch string) string {
	if arch == "x86" {
		if url := strings.TrimSpace(config.Cfg.VirtioURLX86); url != "" {
			return url
		}
		return virtioX86URL
	}
	if url := strings.TrimSpace(config.Cfg.VirtioURLX64); url != "" {
		return url
	}
	return virtioX64URL
}

// virtioGuestToolsInstalled 检查是否已安装 virtio 驱动包（读卸载注册表项）
func virtioGuestToolsInstalled() bool {
	const uninstallPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`

	for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
		key, err := registry.OpenKey(registry.LOCAL_MACHINE, uninstallPath, registry.READ|view)
		if err != nil {
			continue
		}

		names, errNames := key.ReadSubKeyNames(-1)
		if errNames == nil {
			for _, name := range names {
				sub, errSub := registry.OpenKey(registry.LOCAL_MACHINE, uninstallPath+`\`+name, registry.QUERY_VALUE|view)
				if errSub != nil {
					continue
				}
				display, _, errDisplay := sub.GetStringValue("DisplayName")
				_ = sub.Close()
				if errDisplay == nil && strings.Contains(strings.ToLower(display), "virtio") {
					_ = key.Close()
					return true
				}
			}
		}
		_ = key.Close()
	}

	return false
}

// pnpEntry PnP 设备信息
type pnpEntry struct {
	deviceID string
	status   string
	code     int
}

// virtioDriverState 返回是否存在 VirtIO 设备、是否有设备缺少驱动
func virtioDriverState() (found bool, missing bool) {
	entries := queryVirtioDevices()
	if len(entries) == 0 {
		return false, false
	}

	for _, entry := range entries {
		if entry.code != 0 {
			return true, true
		}
		status := strings.ToLower(entry.status)
		if status != "" && status != "ok" && status != "degraded" {
			return true, true
		}
	}

	return true, false
}

// queryVirtioDevices 查询 VirtIO 设备（优先 wmic，失败回退 PowerShell）
func queryVirtioDevices() []pnpEntry {
	result := runShell(`wmic path Win32_PnPEntity get DeviceID,ConfigManagerErrorCode,Status /format:csv`)
	if result.Returncode == 0 && result.Err == nil {
		if entries := parseVirtioCSV(result.Stdout); len(entries) > 0 {
			return entries
		}
	}

	return queryVirtioDevicesPowerShell()
}

// parseVirtioCSV 解析 wmic 的 CSV 输出
func parseVirtioCSV(output string) []pnpEntry {
	entries := make([]pnpEntry, 0, 2)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || !strings.Contains(strings.ToUpper(line), virtioPciVendor) {
			continue
		}

		fields := strings.Split(line, ",")
		if len(fields) < 4 {
			continue
		}

		code, _ := strconv.Atoi(strings.TrimSpace(fields[1]))
		status := strings.TrimSpace(fields[len(fields)-1])
		deviceID := strings.Join(fields[2:len(fields)-1], ",")

		entries = append(entries, pnpEntry{deviceID: deviceID, status: status, code: code})
	}

	return entries
}

// queryVirtioDevicesPowerShell wmic 不可用时的兜底查询
func queryVirtioDevicesPowerShell() []pnpEntry {
	const script = `Get-CimInstance Win32_PnPEntity | Where-Object { $_.DeviceID -like '*VEN_1AF4*' } | ForEach-Object { $_.DeviceID + ',' + $_.ConfigManagerErrorCode + ',' + $_.Status }`

	result := runShell(`powershell -NoProfile -Command "` + script + `"`)
	if result.Returncode != 0 || result.Err != nil {
		return nil
	}

	entries := make([]pnpEntry, 0, 2)
	for _, line := range strings.Split(result.Stdout, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || !strings.Contains(strings.ToUpper(line), virtioPciVendor) {
			continue
		}

		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			continue
		}

		code, _ := strconv.Atoi(strings.TrimSpace(fields[1]))
		entries = append(entries, pnpEntry{
			deviceID: fields[0],
			status:   strings.TrimSpace(fields[2]),
			code:     code,
		})
	}

	return entries
}

// ensureVirtioInstaller 确保驱动包已下载到本地（已存在则复用）
func ensureVirtioInstaller(url, path string) error {
	if info, err := os.Stat(path); err == nil && info.Size() > 1024*1024 {
		logger.Debugf("[VirtIO] 复用已下载的驱动包: %s", path)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tempPath := path + ".download"
	if err := downloadFile(url, tempPath, 10*time.Minute); err != nil {
		_ = os.Remove(tempPath)
		return err
	}

	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}

	return nil
}

// downloadFile 下载文件到指定路径
func downloadFile(url, path string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	file, err := os.Create(path)
	if err != nil {
		return err
	}

	written, copyErr := io.Copy(file, resp.Body)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written < 1024*1024 {
		return fmt.Errorf("文件过小(%d 字节)，可能下载失败", written)
	}

	return nil
}

// installVirtioMSI 静默安装驱动包
func installVirtioMSI(path string) error {
	command := fmt.Sprintf(`msiexec /i "%s" /qn /norestart`, path)

	switch code := runShellWithTimeout(command, 10*time.Minute); code {
	case 0, 3010, 1638: // 成功 / 成功需重启 / 已安装更高版本
		return nil
	default:
		return fmt.Errorf("msiexec 返回码 %d", code)
	}
}

// virtioAttemptMarker 记录安装失败时间，避免每次启动都重复下载安装
func virtioAttemptMarker() string {
	return filepath.Join(apppath.ToolDir(), ".virtio_attempt")
}

func recentInstallAttempt() bool {
	info, err := os.Stat(virtioAttemptMarker())
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < 6*time.Hour
}

func markInstallAttempt() {
	_ = os.WriteFile(virtioAttemptMarker(), []byte(time.Now().Format(time.RFC3339)), 0o644)
}
