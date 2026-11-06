//go:build windows

package remotedesk

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"CloudInit/logger"
)

// anyDeskDownloadURL 官方下载地址（tools.json 未下发 AnyDesk 时的兜底）
const anyDeskDownloadURL = "https://download.anydesk.com/AnyDesk.exe"

// ensureAnyDesk 确保 AnyDesk 已下载并安装为服务，返回可执行文件路径
func (m *Manager) ensureAnyDesk() string {
	exePath := filepath.Join(m.toolDir, "AnyDesk.exe")

	if _, err := os.Stat(exePath); err != nil {
		logger.Warnf("[AnyDesk] 客户端不存在，尝试从官网下载")
		if err := downloadExecutable(anyDeskDownloadURL, exePath, 10*time.Minute); err != nil {
			logger.Warnf("[AnyDesk] 下载失败: %v", err)
			return ""
		}
	}

	// 静默安装为服务并开机自启（已安装时幂等）
	code, err := runHidden(exePath, 180*time.Second,
		"--install", `C:\ProgramData\AnyDesk`, "--start-with-win", "--silent")
	if err != nil || code != 0 {
		logger.Debugf("[AnyDesk] 静默安装返回码 %d: %v", code, err)
	}

	return exePath
}

// ensureAnyDeskPassword 返回本机固定密码，必要时通过 CLI 写入 AnyDesk
//
// AnyDesk 的无人值守密码在配置里是加盐哈希、无法反解，
// 因此由 Agent 生成随机密码并调用 --set-password 写入，再上报该密码。
func (m *Manager) ensureAnyDeskPassword(exePath string) string {
	passwordPath := filepath.Join(m.toolDir, ".anydesk_pwd")

	if data, err := os.ReadFile(passwordPath); err == nil {
		if password := strings.TrimSpace(string(data)); validPassword(password) {
			return password
		}
	}

	password := randomPassword(10)
	if err := os.WriteFile(passwordPath, []byte(password), 0o600); err != nil {
		logger.Warnf("[AnyDesk] 保存密码文件失败: %v", err)
	}

	code, err := runHidden(exePath, 60*time.Second, "--set-password", password)
	if err != nil || code != 0 {
		logger.Warnf("[AnyDesk] 设置无人值守密码失败(返回码 %d): %v", code, err)
		return ""
	}

	logger.Infof("[AnyDesk] 已设置无人值守密码")
	return password
}

// runHidden 静默执行外部程序，返回退出码
func runHidden(exePath string, timeout time.Duration, args ...string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, exePath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_CONSOLE,
	}

	err := cmd.Run()
	if err == nil {
		return 0, nil
	}

	var exitErr *exec.ExitError
	if ok := asExitErr(err, &exitErr); ok {
		return exitErr.ExitCode(), err
	}
	return -1, err
}

// asExitErr 包装 errors.As，避免引入额外导入
func asExitErr(err error, target **exec.ExitError) bool {
	exitErr, ok := err.(*exec.ExitError)
	if ok {
		*target = exitErr
	}
	return ok
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
