// 自动更新模块 - 定期从GitHub拉取最新tag版本并热更新
//
// 工作流程：
// 1. 后台守护协程每24h（可配置）检查一次GitHub Release
// 2. 发现新版本后下载对应平台的二进制文件
// 3. 替换当前可执行文件并重启服务
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"CloudInit/apppath"
	"CloudInit/config"
	"CloudInit/logger"
	"CloudInit/platform"
)

// githubProxies GitHub下载代理列表，按优先级顺序尝试
var githubProxies = []string{
	"https://github.com/",
	"https://github.524228.xyz/",
	"https://ghfast.top/https://github.com/",
	"https://ghproxy.net/https://github.com/",
	"https://gh-proxy.org/https://github.com/",
}

// githubAPIProxies GitHub API代理列表，按优先级顺序尝试
var githubAPIProxies = []string{
	"https://api.github.com/",
	"https://api.github.524228.xyz/",
	"https://ghfast.top/https://api.github.com/",
	"https://ghproxy.net/https://api.github.com/",
	"https://gh-proxy.org/https://api.github.com/",
}

// AutoUpdate 自动更新服务
type AutoUpdate struct {
	running int32
	done    chan struct{}
	api     *http.Client
	down    *http.Client
}

// NewAutoUpdate 创建自动更新服务
func NewAutoUpdate() *AutoUpdate {
	downTransport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   60 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   60 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}

	return &AutoUpdate{
		api:  &http.Client{Timeout: 30 * time.Second},
		down: &http.Client{Transport: downTransport},
	}
}

// repoURL 获取更新仓库地址（支持配置覆盖）
func (a *AutoUpdate) repoURL() string { return config.Cfg.UpdateRepo }

// checkInterval 获取检查间隔（支持配置覆盖）
func (a *AutoUpdate) checkInterval() int { return config.Cfg.UpdateInterval }

// versionFile 版本文件路径
func (a *AutoUpdate) versionFile() string {
	return filepath.Join(apppath.BaseDir(), ".version")
}

// Start 启动自动更新检查
func (a *AutoUpdate) Start() {
	atomic.StoreInt32(&a.running, 1)
	a.done = make(chan struct{})

	go a.updateLoop()

	logger.Infof("[自动更新] 后台检查已启动，周期: %dh", a.checkInterval()/3600)
}

// Stop 停止自动更新检查
func (a *AutoUpdate) Stop() {
	atomic.StoreInt32(&a.running, 0)
	if a.done == nil {
		return
	}
	select {
	case <-a.done:
	case <-time.After(5 * time.Second):
	}
}

// updateLoop 更新检查主循环（运行在后台协程）
func (a *AutoUpdate) updateLoop() {
	defer close(a.done)

	for atomic.LoadInt32(&a.running) == 1 {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Errorf("[自动更新] 本轮检查异常（将在下个周期重试）: %v", r)
				}
			}()
			a.checkAndUpdate()
		}()

		// 可中断的等待，每秒检查一次退出标志
		for i := 0; i < a.checkInterval(); i++ {
			if atomic.LoadInt32(&a.running) == 0 {
				return
			}
			time.Sleep(time.Second)
		}
	}
}

// checkAndUpdate 检查远程版本并决定是否更新
func (a *AutoUpdate) checkAndUpdate() {
	currentVersion := a.currentVersion()
	latestInfo := a.latestRelease()

	if len(latestInfo) == 0 {
		return
	}

	latestVersion := mapString(latestInfo, "tag_name")
	if latestVersion == "" {
		return
	}

	// 忽略 beta/prerelease 版本
	if mapBool(latestInfo, "prerelease") {
		logger.Debugf("[自动更新] 最新版本(%s)为预发布版本，跳过", latestVersion)
		return
	}

	// 工具更新与主程序版本无关，先按工具自身版本同步
	a.syncTools(latestInfo)

	// 版本相同则跳过
	if latestVersion == currentVersion {
		logger.Debugf("[自动更新] 当前版本(%s)已是最新", currentVersion)
		return
	}

	// 语义化版本比较，确保远程版本确实更新
	if !isNewerVersion(latestVersion, currentVersion) {
		logger.Debugf("[自动更新] 远程版本(%s)不高于当前版本(%s)，跳过", latestVersion, currentVersion)
		return
	}

	logger.Infof("[自动更新] 发现新版本: %s -> %s", currentVersion, latestVersion)
	a.downloadAndUpdate(latestInfo)
}

// isNewerVersion 语义化版本比较，判断远程版本是否高于本地版本
func isNewerVersion(remote, local string) bool {
	if local == "unknown" {
		return true
	}

	remoteParts, okRemote := parseVersion(remote)
	localParts, okLocal := parseVersion(local)
	if !okRemote || !okLocal {
		// 解析失败，回退为简单不等判断
		return remote != local
	}

	maxLen := len(remoteParts)
	if len(localParts) > maxLen {
		maxLen = len(localParts)
	}

	for i := 0; i < maxLen; i++ {
		remotePart, localPart := 0, 0
		if i < len(remoteParts) {
			remotePart = remoteParts[i]
		}
		if i < len(localParts) {
			localPart = localParts[i]
		}
		if remotePart != localPart {
			return remotePart > localPart
		}
	}

	return false
}

// parseVersion 将版本字符串解析为数字列表，如 'v1.2.3' -> [1, 2, 3]
func parseVersion(ver string) ([]int, bool) {
	ver = strings.TrimSpace(ver)
	ver = strings.TrimLeft(ver, "vV")

	parts := make([]int, 0, 4)
	for _, part := range strings.Split(ver, ".") {
		if value, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			parts = append(parts, value)
			continue
		}

		// 处理类似 "3-rc1" 的情况，取数字部分
		digits := ""
		for _, ch := range part {
			if ch >= '0' && ch <= '9' {
				digits += string(ch)
			} else {
				break
			}
		}

		if digits == "" {
			parts = append(parts, 0)
			continue
		}
		value, err := strconv.Atoi(digits)
		if err != nil {
			return nil, false
		}
		parts = append(parts, value)
	}

	return parts, true
}

// currentVersion 读取本地版本号文件
func (a *AutoUpdate) currentVersion() string {
	data, err := os.ReadFile(a.versionFile())
	if err == nil {
		return strings.TrimSpace(string(data))
	}
	if !os.IsNotExist(err) {
		logger.Warnf("[自动更新] 版本文件读取失败: %v", err)
	}
	return "unknown"
}

// saveCurrentVersion 保存版本号到本地文件
func (a *AutoUpdate) saveCurrentVersion(version string) {
	if err := os.WriteFile(a.versionFile(), []byte(version), 0o644); err != nil {
		logger.Errorf("[自动更新] 版本文件保存失败: %v", err)
	}
}

// latestRelease 获取最新正式版本信息
// 优先使用镜像站（EdgeOne），不可用时回退 GitHub
func (a *AutoUpdate) latestRelease() map[string]interface{} {
	if mirror := strings.TrimSpace(config.Cfg.MirrorURL); mirror != "" {
		if release, ok := a.mirrorRelease(mirror); ok {
			return release
		}
		logger.Debugf("[自动更新] 镜像站不可用，回退 GitHub")
	}
	return a.githubRelease()
}

// mirrorRelease 从镜像站读取 latest.json
func (a *AutoUpdate) mirrorRelease(mirror string) (map[string]interface{}, bool) {
	baseURL := strings.TrimRight(mirror, "/")
	url := baseURL + "/latest.json"

	logger.Debugf("[自动更新] 尝试镜像: %s", url)

	resp, err := a.api.Get(url)
	if err != nil {
		if isTimeoutError(err) {
			logger.Debugf("[自动更新] 镜像请求超时")
		} else {
			logger.Debugf("[自动更新] 镜像请求失败: %v", err)
		}
		return nil, false
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		logger.Debugf("[自动更新] 镜像 HTTP %d", resp.StatusCode)
		return nil, false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Debugf("[自动更新] 镜像响应读取失败: %v", err)
		return nil, false
	}

	var manifest struct {
		Version string `json:"version"`
		Assets  []struct {
			Name   string `json:"name"`
			File   string `json:"file"`
			SHA256 string `json:"sha256"`
		} `json:"assets"`
	}
	if err = json.Unmarshal(body, &manifest); err != nil {
		logger.Debugf("[自动更新] 镜像清单解析失败: %v", err)
		return nil, false
	}
	if manifest.Version == "" {
		return nil, false
	}

	assets := make([]interface{}, 0, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		if asset.Name == "" {
			continue
		}
		file := asset.File
		if file == "" {
			file = asset.Name
		}
		assets = append(assets, map[string]interface{}{
			"name":                 asset.Name,
			"browser_download_url": baseURL + "/" + file,
			"sha256":               asset.SHA256,
		})
	}

	logger.Debugf("[自动更新] 镜像版本: %s（%d 个资产）", manifest.Version, len(assets))

	return map[string]interface{}{
		"tag_name":   manifest.Version,
		"prerelease": false,
		"assets":     assets,
	}, true
}

// githubRelease 从GitHub API获取最新正式Release信息，按代理列表依次尝试
func (a *AutoUpdate) githubRelease() map[string]interface{} {
	// 将 /releases/latest 替换为 /releases 以获取完整列表用于过滤
	releaseURL := strings.ReplaceAll(a.repoURL(), "/releases/latest", "/releases")

	for _, proxy := range githubAPIProxies {
		url := releaseURL
		if strings.HasPrefix(releaseURL, "https://api.github.com/") {
			url = strings.Replace(releaseURL, "https://api.github.com/", proxy, 1)
		}

		logger.Debugf("[自动更新] 尝试API: %s", url)

		resp, err := a.api.Get(url)
		if err != nil {
			if isTimeoutError(err) {
				logger.Debugf("[自动更新] API超时 - 切换下一个代理")
			} else if isConnectionError(err) {
				logger.Debugf("[自动更新] API连接失败 - 切换下一个代理")
			} else {
				logger.Debugf("[自动更新] API请求异常: %v - 切换下一个代理", err)
			}
			continue
		}

		if resp.StatusCode != 200 {
			logger.Debugf("[自动更新] HTTP %d - 切换下一个API代理", resp.StatusCode)
			_ = resp.Body.Close()
			continue
		}

		logger.Debugf("[自动更新] API请求成功 (via %s)", strings.TrimRight(proxy, "/"))

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			logger.Debugf("[自动更新] API请求异常: %v - 切换下一个代理", readErr)
			continue
		}

		var parsed interface{}
		if err = json.Unmarshal(body, &parsed); err != nil {
			logger.Debugf("[自动更新] API请求异常: %v - 切换下一个代理", err)
			continue
		}

		// 如果返回的是列表，过滤出第一个非prerelease且非beta的版本
		if releases, ok := parsed.([]interface{}); ok {
			for _, item := range releases {
				release, okRelease := item.(map[string]interface{})
				if !okRelease {
					continue
				}
				tag := strings.ToLower(mapString(release, "tag_name"))
				if mapBool(release, "prerelease") {
					continue
				}
				if mapBool(release, "draft") {
					continue
				}
				if strings.Contains(tag, "beta") || strings.Contains(tag, "alpha") || strings.Contains(tag, "rc") {
					continue
				}
				return release
			}
			return map[string]interface{}{} // 没有找到正式版本
		}

		// 兼容直接返回单个release的情况
		if release, okRelease := parsed.(map[string]interface{}); okRelease {
			return release
		}

		return map[string]interface{}{}
	}

	logger.Warnf("[自动更新] 所有API代理均无法获取Release信息")
	return map[string]interface{}{}
}

// downloadAndUpdate 下载新版本并执行热更新
func (a *AutoUpdate) downloadAndUpdate(releaseInfo map[string]interface{}) {
	system := runtime.GOOS
	assets := mapSlice(releaseInfo, "assets")
	tagName := mapString(releaseInfo, "tag_name")

	// 匹配当前平台的资产文件
	targetAsset := findPlatformAsset(assets, system)
	if targetAsset == nil {
		logger.Warnf("[自动更新] 未找到平台(%s)对应的更新文件", system)
		return
	}

	downloadURL := mapString(targetAsset, "browser_download_url")
	if downloadURL == "" {
		logger.Errorf("[自动更新] 资产文件缺少下载地址")
		return
	}

	exePath := apppath.ExePath()
	tempPath := exePath + ".update"

	// 通过代理列表尝试下载
	if !a.downloadWithProxy(downloadURL, tempPath) {
		logger.Errorf("[自动更新] 所有下载渠道均失败")
		return
	}

	// 镜像清单带 sha256 时校验完整性
	if expected := mapString(targetAsset, "sha256"); expected != "" {
		if !verifyFileSHA256(tempPath, expected) {
			logger.Errorf("[自动更新] 安装包校验失败，已丢弃")
			cleanupTemp(tempPath)
			return
		}
	}

	logger.Infof("[自动更新] 下载完成，执行替换...")

	// 按平台执行文件替换
	var err error
	switch system {
	case "linux", "darwin":
		err = replaceUnix(exePath, tempPath)
	case "windows":
		err = replaceWindows(exePath, tempPath)
	default:
		logger.Warnf("[自动更新] 不支持的平台: %s", system)
		return
	}

	if err != nil {
		logger.Errorf("[自动更新] 文件操作异常: %v", err)
		cleanupTemp(tempPath)
		return
	}

	// 记录新版本号
	a.saveCurrentVersion(tagName)
	logger.Infof("[自动更新] 已更新至: %s", tagName)

	// 重启服务使新版本生效
	restartService(system)
}

// downloadWithProxy 下载文件：GitHub 地址按代理列表依次尝试，其它地址（镜像站）直连
func (a *AutoUpdate) downloadWithProxy(originalURL, savePath string) bool {
	if !strings.HasPrefix(originalURL, "https://github.com/") {
		return a.downloadOnce(originalURL, savePath)
	}

	for _, proxy := range githubProxies {
		url := strings.Replace(originalURL, "https://github.com/", proxy, 1)
		if a.downloadOnce(url, savePath) {
			logger.Infof("[自动更新] 下载成功 (via %s)", strings.TrimRight(proxy, "/"))
			return true
		}
	}

	return false
}

// downloadOnce 执行一次下载
func (a *AutoUpdate) downloadOnce(url, savePath string) bool {
	logger.Infof("[自动更新] 尝试下载: %s", url)

	resp, err := a.down.Get(url)
	if err != nil {
		logDownloadError(err)
		cleanupTemp(savePath)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		logger.Debugf("[自动更新] HTTP %d - 切换下一个代理", resp.StatusCode)
		return false
	}

	// 流式写入文件
	file, err := os.Create(savePath)
	if err != nil {
		logger.Debugf("[自动更新] 下载异常: %v - 切换下一个代理", err)
		cleanupTemp(savePath)
		return false
	}

	_, copyErr := io.Copy(file, resp.Body)
	_ = file.Close()

	if copyErr != nil {
		logDownloadError(copyErr)
		cleanupTemp(savePath)
		return false
	}

	return true
}

// logDownloadError 输出下载错误日志
func logDownloadError(err error) {
	if isTimeoutError(err) {
		logger.Debugf("[自动更新] 超时 - 切换下一个代理")
	} else if isConnectionError(err) {
		logger.Debugf("[自动更新] 连接失败 - 切换下一个代理")
	} else {
		logger.Debugf("[自动更新] 下载异常: %v - 切换下一个代理", err)
	}
}

// getArchTag 获取当前机器架构标识（x64/x86/a64）
func getArchTag() string {
	switch runtime.GOARCH {
	case "arm64":
		return "a64"
	case "386":
		return "x86"
	default:
		return "x64"
	}
}

// findPlatformAsset 根据当前平台和架构匹配对应的Release资产
func findPlatformAsset(assets []interface{}, system string) map[string]interface{} {
	arch := getArchTag()
	allArchTags := []string{"x64", "x86", "a64"}

	if system == "windows" {
		isLegacy := platform.IsLegacyWindows()
		osTag := "win10"
		if isLegacy {
			osTag = "win78"
		}

		// 优先匹配: CloudInit-win10-x64 或 CloudInit-win78-a64 等
		for _, asset := range assets {
			name := strings.ToLower(assetName(asset))
			if strings.Contains(name, osTag) && strings.Contains(name, arch) {
				return assetMap(asset)
			}
		}

		// 回退: CloudInit-win10 或 CloudInit-win78（不带任何架构后缀，默认x64）
		if arch == "x64" {
			for _, asset := range assets {
				name := strings.ToLower(assetName(asset))
				if strings.Contains(name, osTag) && !containsAny(name, allArchTags) {
					return assetMap(asset)
				}
			}
		}

		// 再回退: 只要包含对应os_tag的任意资产（优先选不带架构的）
		for _, asset := range assets {
			name := strings.ToLower(assetName(asset))
			if strings.Contains(name, osTag) && !containsAny(name, allArchTags) {
				return assetMap(asset)
			}
		}
		for _, asset := range assets {
			name := strings.ToLower(assetName(asset))
			if strings.Contains(name, osTag) {
				return assetMap(asset)
			}
		}

		// Win7/8找不到时尝试通用Windows包
		if isLegacy {
			logger.Warnf("[自动更新] 未找到Win7/8专用更新包(win78)，尝试使用通用Windows包")
			for _, asset := range assets {
				name := strings.ToLower(assetName(asset))
				if (strings.Contains(name, "win10") || strings.Contains(name, "windows")) &&
					!strings.Contains(name, "win78") {
					return assetMap(asset)
				}
			}
		}
	} else {
		osTag := system
		switch system {
		case "linux":
			osTag = "linux"
		case "darwin":
			osTag = "macos"
		}

		// 优先匹配: CloudInit-linux-x64 或 CloudInit-macos-a64 等
		for _, asset := range assets {
			name := strings.ToLower(assetName(asset))
			if strings.Contains(name, osTag) && strings.Contains(name, arch) {
				return assetMap(asset)
			}
		}

		// 回退: CloudInit-linux 或 CloudInit-macos（不带任何架构后缀，默认x64）
		if arch == "x64" {
			for _, asset := range assets {
				name := strings.ToLower(assetName(asset))
				if strings.Contains(name, osTag) && !containsAny(name, allArchTags) {
					return assetMap(asset)
				}
			}
		}

		// 再回退: 只要包含对应os_tag的任意资产（优先选不带架构的）
		for _, asset := range assets {
			name := strings.ToLower(assetName(asset))
			if strings.Contains(name, osTag) && !containsAny(name, allArchTags) {
				return assetMap(asset)
			}
		}
		for _, asset := range assets {
			name := strings.ToLower(assetName(asset))
			if strings.Contains(name, osTag) {
				return assetMap(asset)
			}
		}
	}

	// 兜底：仅有一个资产时直接使用
	if len(assets) == 1 {
		return assetMap(assets[0])
	}
	return nil
}

// replaceUnix Unix系统（Linux/macOS）文件替换
func replaceUnix(exePath, tempPath string) error {
	backupPath := exePath + ".bak"
	if _, err := os.Stat(backupPath); err == nil {
		if err = os.Remove(backupPath); err != nil {
			return err
		}
	}
	if _, err := os.Stat(exePath); err == nil {
		if err = os.Rename(exePath, backupPath); err != nil {
			return err
		}
	}
	if err := os.Rename(tempPath, exePath); err != nil {
		return err
	}
	return os.Chmod(exePath, 0o755)
}

// replaceWindows Windows系统：生成批处理脚本，等待旧进程退出后替换文件并重启
func replaceWindows(exePath, tempPath string) error {
	batchContent := "@echo off\r\n" +
		":wait_exit\r\n" +
		fmt.Sprintf("del /F \"%s\" >nul 2>&1\r\n", exePath) +
		fmt.Sprintf("if exist \"%s\" (\r\n", exePath) +
		"    timeout /t 1 /nobreak >nul\r\n" +
		"    goto wait_exit\r\n" +
		")\r\n" +
		fmt.Sprintf("move /Y \"%s\" \"%s\"\r\n", tempPath, exePath) +
		"if errorlevel 1 (\r\n" +
		"    timeout /t 2 /nobreak >nul\r\n" +
		fmt.Sprintf("    move /Y \"%s\" \"%s\"\r\n", tempPath, exePath) +
		")\r\n" +
		fmt.Sprintf("start \"CloudInit\" \"%s\"\r\n", exePath) +
		"timeout /t 3 /nobreak >nul\r\n" +
		"del \"%~f0\"\r\n"

	batchPath := filepath.Join(filepath.Dir(exePath), "_update.bat")
	return os.WriteFile(batchPath, []byte(batchContent), 0o644)
}

// restartService 重启服务使新版本生效
func restartService(system string) {
	if system == "linux" || system == "darwin" {
		logger.Infof("[自动更新] 通过systemctl重启服务...")
		// 使用Popen异步发起重启，然后立即退出当前进程
		cmd := exec.Command("systemctl", "restart", "ServerInit")
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			logger.Errorf("[自动更新] 重启服务失败: %v", err)
			return
		}
		os.Exit(0)
	}

	if system == "windows" {
		logger.Infof("[自动更新] 通过批处理重启...")
		exePath := apppath.ExePath()
		batchPath := filepath.Join(filepath.Dir(exePath), "_update.bat")
		if _, err := os.Stat(batchPath); err == nil {
			if err = startDetachedBatch(batchPath); err != nil {
				logger.Errorf("[自动更新] 重启服务失败: %v", err)
				return
			}
			// 等待一小段时间确保批处理进程已创建
			time.Sleep(500 * time.Millisecond)
			// 强制退出当前进程，释放exe文件锁
			os.Exit(0)
		}
	}
}

// cleanupTemp 清理下载的临时文件
func cleanupTemp(tempPath string) {
	if _, err := os.Stat(tempPath); err == nil {
		_ = os.Remove(tempPath)
	}
}

// assetName 读取资产文件名
func assetName(asset interface{}) string {
	item, ok := asset.(map[string]interface{})
	if !ok {
		return ""
	}
	return mapString(item, "name")
}

// assetMap 将资产转换为 map
func assetMap(asset interface{}) map[string]interface{} {
	if item, ok := asset.(map[string]interface{}); ok {
		return item
	}
	return nil
}

// containsAny 判断字符串是否包含任意一个标记
func containsAny(name string, tags []string) bool {
	for _, tag := range tags {
		if strings.Contains(name, tag) {
			return true
		}
	}
	return false
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

// mapBool 读取 map 中的布尔字段
func mapBool(data map[string]interface{}, key string) bool {
	value, ok := data[key]
	if !ok || value == nil {
		return false
	}
	if b, okBool := value.(bool); okBool {
		return b
	}
	return false
}

// mapSlice 读取 map 中的数组字段
func mapSlice(data map[string]interface{}, key string) []interface{} {
	value, ok := data[key]
	if !ok || value == nil {
		return nil
	}
	if list, okList := value.([]interface{}); okList {
		return list
	}
	return nil
}

// isTimeoutError 判断是否为超时错误
func isTimeoutError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// isConnectionError 判断是否为连接类错误
func isConnectionError(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr)
}
