//go:build windows

package remotedesk

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"CloudInit/logger"
)

const (
	// anyDeskDownloadURL 官方下载地址（tools.json 未下发 AnyDesk 时的兜底）
	anyDeskDownloadURL = "https://download.anydesk.com/AnyDesk.exe"
	// anyDeskPasswordFile 记录已成功写入 AnyDesk 的无人值守密码
	anyDeskPasswordFile = ".anydesk_pwd"
	// anyDeskPasswordState 记录密码写入后配置里密码字段的指纹，指纹一致就不再重写
	anyDeskPasswordState = ".anydesk_pwd_state"
	// anyDeskInstalledMarker 记录已安装客户端的指纹，避免每次启动重复安装
	anyDeskInstalledMarker = ".anydesk_installed"
)

// anyDeskCLIMu 串行化 AnyDesk 命令行调用
//
// --install / --set-password 都会拉起一份完整的 AnyDesk 实例，
// 并发执行时两份实例互抢监听套接字，5.x 客户端会直接崩溃。
var anyDeskCLIMu sync.Mutex

// runAnyDeskCLI 串行执行 AnyDesk 命令行，返回退出码与输出
func runAnyDeskCLI(exePath, input string, timeout time.Duration, args ...string) (int, string, error) {
	anyDeskCLIMu.Lock()
	defer anyDeskCLIMu.Unlock()
	return runHiddenWithInput(exePath, input, timeout, args...)
}

// ensureAnyDesk 确保 AnyDesk 已安装为服务，返回可执行文件路径
//
// 只在「尚未安装」或「tool 目录里的客户端已更新」时执行 --install：
// 老版本每次启动都安装一遍，cmdif 被反复拉起，AnyDesk 5.x 自定义客户端上
// 会崩溃（日志里的 cmdif_i / eh.handler / Abnormal program termination）。
func (m *Manager) ensureAnyDesk() string {
	exePath := filepath.Join(m.toolDir, "AnyDesk.exe")
	markerPath := filepath.Join(m.toolDir, anyDeskInstalledMarker)
	installed := findInstalledAnyDesk()

	if installed != "" {
		switch {
		case anyDeskInstallUpToDate(exePath, markerPath):
			return installed
		case !fileExists(exePath):
			// tool 目录里的客户端被删了，但系统里已安装：保持现状
			return installed
		case !fileExists(markerPath):
			// 镜像预装/老版本安装没有指纹：补写后复用，避免重复安装
			writeAnyDeskMarker(exePath, markerPath)
			return installed
		}
		// 指纹不一致：tool 目录里的客户端已更新，走安装升级
	} else if anyDeskInstallUpToDate(exePath, markerPath) {
		// 安装目录不在默认位置时 findInstalledAnyDesk 找不到，但指纹说明已装过
		return exePath
	}

	if _, err := os.Stat(exePath); err != nil {
		logger.Warnf("[AnyDesk] 客户端不存在，尝试从官网下载")
		if err := downloadExecutable(anyDeskDownloadURL, exePath, 10*time.Minute); err != nil {
			logger.Warnf("[AnyDesk] 下载失败: %v", err)
			return ""
		}
	}

	// 必须装到标准目录：C:\ProgramData\AnyDesk 是 AnyDesk 自己的配置目录（system.conf）
	code, output, err := runAnyDeskCLI(exePath, "", 180*time.Second,
		"--install", anyDeskInstallDir(), "--start-with-win", "--silent")
	if err != nil || code != 0 {
		// --silent 会吞掉错误弹窗，把输出记到日志才能定位
		logger.Warnf("[AnyDesk] 静默安装失败(返回码 %d): %v %s", code, err, output)
	} else {
		writeAnyDeskMarker(exePath, markerPath)
	}

	if installed := findInstalledAnyDesk(); installed != "" {
		logger.Infof("[AnyDesk] 使用已安装客户端: %s", installed)
		return installed
	}
	return exePath
}

// fileExists 判断文件是否存在
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// launchAnyDesk 启动 AnyDesk
//
// 已注册为服务时只保证服务在运行，客户端进程由 AnyDesk 服务自己维护：
// 再从 Agent 里拉起一份会与它共用 C:\ProgramData\AnyDesk 抢监听套接字，
// 日志表现为 app.service 反复设置 listen sockets、anynet.relay_connector
// 连续 bootstrap，最后 Fatal Program Failure。
func (m *Manager) launchAnyDesk(exePath string) {
	if ensureAnyDeskService() {
		logger.Infof("[AnyDesk] 客户端已作为服务运行，跳过拉起进程")
		return
	}

	// 已有实例时不再拉起：同一台机器上两份 AnyDesk 就是上面的崩溃场景
	if len(findProcessIDs("AnyDesk.exe")) > 0 {
		logger.Infof("[AnyDesk] 客户端进程已存在，跳过拉起")
		return
	}

	// 没装服务（便携/自定义包）：Session 0 没有交互桌面，只能拉起后台实例，
	// 直接启动 GUI 实例会初始化失败并弹 Fatal Program Failure。
	// 参数与服务注册的命令行一致（AnyDesk.exe --service），--silent 禁止弹窗。
	args := make([]string, 0, 2)
	if runningInSessionZero() {
		args = append(args, "--service", "--silent")
	}

	cmd := exec.Command(exePath, args...)
	cmd.Dir = filepath.Dir(exePath)
	if err := cmd.Start(); err != nil {
		logger.Debugf("[AnyDesk] 启动客户端失败: %v", err)
		return
	}
	logger.Infof("[AnyDesk] 已启动客户端: %s", exePath)

	if len(args) == 0 {
		go hideWindowsOfProcess("AnyDesk.exe", 15)
	}
}

// ensureAnyDeskService 确保 AnyDesk 自带服务存在且在运行，返回是否找到服务
func ensureAnyDeskService() bool {
	scm, err := mgr.Connect()
	if err != nil {
		logger.Debugf("[AnyDesk] 连接服务管理器失败: %v", err)
		return false
	}
	defer scm.Disconnect()

	name := findAnyDeskService(scm)
	if name == "" {
		return false
	}
	runAnyDeskService(scm, name)
	return true
}

// findAnyDeskService 返回 AnyDesk 自带服务名，未安装返回空
func findAnyDeskService(scm *mgr.Mgr) string {
	names, err := scm.ListServices()
	if err != nil {
		logger.Debugf("[AnyDesk] 枚举服务失败: %v", err)
		return ""
	}

	for _, name := range names {
		if isAnyDeskService(name) {
			return name
		}
	}
	// 自定义客户端服务名可能不含 AnyDesk，再按可执行文件路径兜底
	for _, name := range names {
		if anyDeskServiceBinary(scm, name) {
			return name
		}
	}
	return ""
}

// anyDeskServiceRunning 判断 AnyDesk 服务是否已进入运行状态
func anyDeskServiceRunning() bool {
	scm, err := mgr.Connect()
	if err != nil {
		return false
	}
	defer scm.Disconnect()

	name := findAnyDeskService(scm)
	if name == "" {
		return false
	}

	service, err := scm.OpenService(name)
	if err != nil {
		return false
	}
	defer service.Close()

	status, err := service.Query()
	return err == nil && status.State == svc.Running
}

// anyDeskServiceReady 确保 CLI 执行前服务已就绪
//
// 服务刚被拉起时立刻执行 --set-password，AnyDesk 会再创建一份实例抢监听套接字，
// 因此最多等 5 秒；没有服务的便携版直接返回 true。
func anyDeskServiceReady() bool {
	if !ensureAnyDeskService() {
		return true
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if anyDeskServiceRunning() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// isAnyDeskService 服务名含 AnyDesk 即认为是 AnyDesk 自带服务
func isAnyDeskService(name string) bool {
	return strings.Contains(strings.ToLower(name), "anydesk")
}

// anyDeskServiceBinary 判断服务是否指向 AnyDesk 客户端（自定义客户端服务名可能不含 AnyDesk）
func anyDeskServiceBinary(scm *mgr.Mgr, name string) bool {
	service, err := scm.OpenService(name)
	if err != nil {
		return false
	}
	defer service.Close()

	conf, err := service.Config()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(conf.BinaryPathName), "anydesk")
}

// runAnyDeskService 启动指定服务（已在运行则跳过）
func runAnyDeskService(scm *mgr.Mgr, name string) {
	service, err := scm.OpenService(name)
	if err != nil {
		logger.Debugf("[AnyDesk] 打开服务 %s 失败: %v", name, err)
		return
	}
	defer service.Close()

	status, err := service.Query()
	if err != nil {
		logger.Debugf("[AnyDesk] 查询服务 %s 状态失败: %v", name, err)
		return
	}
	if status.State == svc.Running || status.State == svc.StartPending {
		return
	}

	if err = service.Start(); err != nil {
		logger.Warnf("[AnyDesk] 启动服务 %s 失败: %v", name, err)
		return
	}
	logger.Infof("[AnyDesk] 已启动服务: %s", name)
}

// runningInSessionZero 判断当前进程是否运行在 Session 0（Windows 服务）
func runningInSessionZero() bool {
	var sessionID uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &sessionID); err != nil {
		return true
	}
	return sessionID == 0
}

// anyDeskInstallUpToDate 判断已安装客户端是否已对应 tool 目录里的文件
func anyDeskInstallUpToDate(exePath, markerPath string) bool {
	data, err := os.ReadFile(exePath)
	if err != nil {
		return false
	}
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(data)
	return strings.TrimSpace(string(marker)) == hex.EncodeToString(sum[:])
}

// writeAnyDeskMarker 记录当前客户端指纹，工具更新后指纹变化才会重新安装
func writeAnyDeskMarker(exePath, markerPath string) {
	data, err := os.ReadFile(exePath)
	if err != nil {
		return
	}
	sum := sha256.Sum256(data)
	if err = os.WriteFile(markerPath, []byte(hex.EncodeToString(sum[:])), 0o644); err != nil {
		logger.Debugf("[AnyDesk] 写入安装标记失败: %v", err)
	}
}

// anyDeskInstallDir 返回标准安装目录（32 位系统退回 Program Files）
func anyDeskInstallDir() string {
	if dir := os.Getenv("ProgramFiles(x86)"); dir != "" {
		return filepath.Join(dir, "AnyDesk")
	}
	if dir := os.Getenv("ProgramFiles"); dir != "" {
		return filepath.Join(dir, "AnyDesk")
	}
	return `C:\Program Files (x86)\AnyDesk`
}

// findInstalledAnyDesk 查找已安装的 AnyDesk 可执行文件
//
// 依次检查：老版本装到 ProgramData\AnyDesk 的副本、标准安装目录、
// 自定义客户端安装目录（AnyDesk-<prefix>\AnyDesk-<prefix>.exe）。
func findInstalledAnyDesk() string {
	candidates := make([]string, 0, 8)

	if programData := os.Getenv("ProgramData"); programData != "" {
		candidates = append(candidates, filepath.Join(programData, "AnyDesk", "AnyDesk.exe"))
		// 老版本/自定义客户端会把客户端与配置放在 ProgramData\AnyDesk-<前缀>
		if matches, err := filepath.Glob(filepath.Join(programData, "AnyDesk*", "AnyDesk*.exe")); err == nil {
			candidates = append(candidates, matches...)
		}
	}
	for _, dir := range anyDeskProgramDirs() {
		candidates = append(candidates, filepath.Join(dir, "AnyDesk", "AnyDesk.exe"))
		if matches, err := filepath.Glob(filepath.Join(dir, "AnyDesk-*", "AnyDesk*.exe")); err == nil {
			candidates = append(candidates, matches...)
		}
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// anyDeskProgramDirs 返回 Program Files 目录（x86 优先）
func anyDeskProgramDirs() []string {
	dirs := make([]string, 0, 2)
	for _, name := range []string{"ProgramFiles(x86)", "ProgramFiles"} {
		if dir := os.Getenv(name); dir != "" {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// ensureAnyDeskPassword 返回本机固定密码，必要时通过 CLI 写入 AnyDesk
//
// AnyDesk 的无人值守密码在配置里是加盐哈希、无法反解，
// 因此由 Agent 生成随机密码并调用 --set-password 写入，再上报该密码。
// 每条 --set-password 都会拉起一份 AnyDesk 实例，因此只在「密码还没生效」
// 时执行：写入成功后记录配置里密码字段的指纹，指纹一致直接复用。
func (m *Manager) ensureAnyDeskPassword(exePath string) string {
	passwordPath := filepath.Join(m.toolDir, anyDeskPasswordFile)
	statePath := filepath.Join(m.toolDir, anyDeskPasswordState)

	saved := ""
	if data, err := os.ReadFile(passwordPath); err == nil {
		if password := strings.TrimSpace(string(data)); validPassword(password) {
			if anyDeskPasswordApplied(statePath, password) {
				return password
			}
			saved = password
		}
	}

	// 写入前确认服务已就绪，避免 CLI 自己再拉起一份实例
	if !anyDeskServiceReady() {
		logger.Warnf("[AnyDesk] 服务尚未就绪，跳过本次无人值守密码写入")
		return ""
	}

	if saved != "" {
		if applyAnyDeskPassword(exePath, saved) {
			writeAnyDeskPasswordState(statePath, saved)
			return saved
		}
		logger.Warnf("[AnyDesk] 已保存的密码写入失败，重新生成")
		_ = os.Remove(passwordPath)
	}

	password := randomPassword(10)
	if !applyAnyDeskPassword(exePath, password) {
		_ = os.Remove(passwordPath)
		return ""
	}
	writeAnyDeskPasswordState(statePath, password)

	if err := os.WriteFile(passwordPath, []byte(password), 0o600); err != nil {
		logger.Warnf("[AnyDesk] 保存密码文件失败: %v", err)
	}

	logger.Infof("[AnyDesk] 已设置无人值守密码")
	return password
}

// applyAnyDeskPassword 通过标准输入把密码写入 AnyDesk
//
// 密码必须走标准输入：echo <pwd> | AnyDesk.exe --set-password。
// 作为命令行参数传入会被当成权限配置文件（profile），5.x 的 cmdif 直接崩溃。
// --silent 让 AnyDesk 不再弹错误框，避免无人值守机器卡在弹窗上。
func applyAnyDeskPassword(exePath, password string) bool {
	code, output, err := runAnyDeskCLI(exePath, password+"\n", 60*time.Second, "--set-password", "--silent")
	if err != nil || code != 0 {
		logger.Warnf("[AnyDesk] 设置无人值守密码失败(返回码 %d): %v %s", code, err, output)
		return false
	}
	return true
}

// anyDeskPasswordApplied 判断密码是否已写入且配置未被改动
func anyDeskPasswordApplied(statePath, password string) bool {
	signature := anyDeskPasswordConfigSignature()
	if signature == "" {
		// 配置里没有密码字段，说明还没写进去
		return false
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == anyDeskPasswordFingerprint(password, signature)
}

// writeAnyDeskPasswordState 记录密码与配置指纹，下次启动可直接复用
func writeAnyDeskPasswordState(statePath, password string) {
	signature := anyDeskPasswordConfigSignature()
	if signature == "" {
		return
	}
	if err := os.WriteFile(statePath, []byte(anyDeskPasswordFingerprint(password, signature)), 0o644); err != nil {
		logger.Debugf("[AnyDesk] 写入密码状态失败: %v", err)
	}
}

// anyDeskPasswordFingerprint 计算密码状态指纹
func anyDeskPasswordFingerprint(password, signature string) string {
	sum := sha256.Sum256([]byte(password + "\x00" + signature))
	return hex.EncodeToString(sum[:])
}

// anyDeskPasswordConfigSignature 返回 AnyDesk 配置里密码字段的指纹
//
// 只看字段名不看值：哈希每次重算都会变，把它算进指纹会导致每次开机都重写密码；
// 而配置被重置（字段消失）时指纹为空，说明需要重新写入。
func anyDeskPasswordConfigSignature() string {
	for _, path := range anyDeskConfigFiles() {
		keys := anyDeskPasswordKeys(path)
		if len(keys) == 0 {
			continue
		}
		sum := sha256.Sum256([]byte(path + "\n" + strings.Join(keys, "\n")))
		return hex.EncodeToString(sum[:])
	}
	return ""
}

// anyDeskPasswordKeys 返回配置文件里的密码字段名（按出现顺序、去重）
func anyDeskPasswordKeys(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	keys := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	for _, line := range strings.Split(string(data), "\n") {
		key, _, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if !strings.Contains(key, "pwd") && !strings.Contains(key, "password") {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

// anyDeskConfigFiles 返回 AnyDesk 可能写入密码的配置文件（system.conf + 当前用户 user.conf）
func anyDeskConfigFiles() []string {
	files := make([]string, 0, 4)
	for _, path := range anyDeskSystemConfs() {
		files = append(files, path)
	}
	if appData := os.Getenv("APPDATA"); appData != "" {
		files = append(files, filepath.Join(appData, "AnyDesk", "user.conf"))
	}
	return files
}

// anyDeskSystemConfs 返回 AnyDesk 全局配置（自定义客户端目录同名带前缀）
func anyDeskSystemConfs() []string {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		return nil
	}

	paths := make([]string, 0, 4)
	if primary := filepath.Join(programData, "AnyDesk", "system.conf"); fileExists(primary) {
		paths = append(paths, primary)
	}
	if matches, err := filepath.Glob(filepath.Join(programData, "AnyDesk*", "system.conf")); err == nil {
		paths = append(paths, matches...)
	}
	return paths
}

// runHiddenWithInput 静默执行外部程序，把 input 写入标准输入，返回退出码与输出
func runHiddenWithInput(exePath, input string, timeout time.Duration, args ...string) (int, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, exePath, args...)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_CONSOLE,
	}

	err := cmd.Run()
	text := strings.TrimSpace(output.String())
	if err == nil {
		return 0, text, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), text, err
	}
	return -1, text, err
}

// downloadExecutable 下载可执行文件（先写临时文件再重命名）
func downloadExecutable(url, dest string, timeout time.Duration) error {
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

	if err = os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	tempPath := dest + ".download"
	file, err := os.Create(tempPath)
	if err != nil {
		return err
	}

	written, copyErr := io.Copy(file, resp.Body)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(tempPath)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tempPath)
		return closeErr
	}
	if written < 1024*1024 {
		_ = os.Remove(tempPath)
		return fmt.Errorf("文件过小(%d 字节)", written)
	}

	return os.Rename(tempPath, dest)
}

// randomPassword 生成指定长度的随机密码（大小写字母 + 数字）
func randomPassword(length int) string {
	const charset = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

	result := make([]byte, length)
	max := big.NewInt(int64(len(charset)))
	for i := range result {
		index, err := rand.Int(rand.Reader, max)
		if err != nil {
			result[i] = charset[0]
			continue
		}
		result[i] = charset[index.Int64()]
	}
	return string(result)
}

// validPassword 判断密码是否可用（字母数字，8-20 位）
func validPassword(value string) bool {
	if len(value) < 8 || len(value) > 20 {
		return false
	}
	for _, ch := range value {
		isDigit := ch >= '0' && ch <= '9'
		isLetter := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
		if !isDigit && !isLetter {
			return false
		}
	}
	return true
}
