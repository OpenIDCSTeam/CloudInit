// Package platform 平台操作抽象层，定义所有平台需要实现的操作接口。
package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"CloudInit/logger"
)

// Platform 平台操作接口
type Platform interface {
	// Shutdown 关机
	Shutdown()
	// Reboot 重启
	Reboot()
	// GetHostname 获取当前主机名
	GetHostname() string
	// SetHostname 设置主机名
	SetHostname(hostname string)
	// SetPassword 设置用户密码
	SetPassword(username, password string)
	// ExtendDisk 磁盘扩容
	ExtendDisk() error
}

// VMToolsHandler Windows 平台可选的 VMware Tools 处理能力
type VMToolsHandler interface {
	UninstallVMToolsIfNotVMware()
}

// WindowsActivator Windows 平台可选的系统激活能力
type WindowsActivator interface {
	ActivateWindows()
}

// VirtioDriverInstaller Windows 平台可选的 virtio 驱动安装能力（KVM 虚拟机）
type VirtioDriverInstaller interface {
	InstallVirtioDrivers()
}

// New 根据当前操作系统返回对应的平台实现实例
func New() (Platform, error) {
	switch runtime.GOOS {
	case "linux":
		return newLinux(), nil
	case "windows":
		return newWindows(), nil
	case "darwin":
		return newDarwin(), nil
	default:
		logger.Errorf("[平台工厂] 不支持的操作系统: %s", runtime.GOOS)
		return nil, fmt.Errorf("不支持的操作系统: %s", runtime.GOOS)
	}
}

// base 提供各平台共用的 hosts 处理
type base struct {
	hostsPath string
}

// updateHosts 更新hosts文件
func (b *base) updateHosts(hostname string) {
	entry := "127.0.0.1\t" + hostname + "\n"

	data, err := os.ReadFile(b.hostsPath)
	if err != nil {
		logger.Errorf("[hosts] 读取或写入 hosts 文件失败: %v", err)
		return
	}

	lines := splitLinesKeepEnd(string(data))

	found := false
	newLines := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		stripped := strings.TrimSpace(line)
		if stripped != "" && !strings.HasPrefix(stripped, "#") {
			parts := strings.Fields(stripped)
			if len(parts) >= 2 && strings.EqualFold(parts[1], hostname) {
				newLines = append(newLines, entry)
				found = true
				continue
			}
		}
		newLines = append(newLines, line)
	}

	if !found {
		inserted := false
		for i, line := range newLines {
			stripped := strings.TrimSpace(line)
			if stripped != "" && strings.Contains(stripped, "127.0.0.1") &&
				strings.Contains(strings.ToLower(stripped), "localhost") {
				newLines = append(newLines[:i+1], append([]string{entry}, newLines[i+1:]...)...)
				inserted = true
				break
			}
		}
		if !inserted {
			newLines = append(newLines, entry)
		}
	}

	content := strings.Join(newLines, "")
	// Python 文本模式写入：Windows 下把 \n 转换为 \r\n
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if runtime.GOOS == "windows" {
		content = strings.ReplaceAll(content, "\n", "\r\n")
	}

	if err = os.WriteFile(b.hostsPath, []byte(content), 0o644); err != nil {
		logger.Errorf("[hosts] 读取或写入 hosts 文件失败: %v", err)
		return
	}

	logger.Infof("[hosts] hosts 文件更新成功: %s", b.hostsPath)
}

// splitLinesKeepEnd 按行切分并保留行尾换行符
func splitLinesKeepEnd(s string) []string {
	if s == "" {
		return nil
	}
	lines := make([]string, 0, 16)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// cmdResult 命令执行结果
type cmdResult struct {
	Stdout     string
	Stderr     string
	Returncode int
	Err        error
}

// runCmd 执行命令的通用封装（等价于 subprocess.run 带 capture_output/text）
func runCmd(name string, args ...string) cmdResult {
	return run(cmd{Build: func() *exec.Cmd { return exec.Command(name, args...) }})
}

// runShell 通过系统 shell 执行命令（等价于 subprocess.run(shell=True)）
func runShell(command string) cmdResult {
	return run(cmd{Build: func() *exec.Cmd { return newShellCmd(command) }})
}

// runShellContext 通过系统 shell 执行命令，支持超时上下文
func runShellContext(ctx context.Context, command string) (cmdResult, error) {
	return runRawResult(newShellCmdContext(ctx, command))
}

// runShellContextRaw 通过系统 shell 执行命令，返回未经解码的原始输出
// 用于需要按本地代码页（如 GBK）解码的场景
func runShellContextRaw(ctx context.Context, command string) (stdout, stderr []byte, code int, err error) {
	return runRaw(newShellCmdContext(ctx, command))
}

// runCmdRaw 执行命令，返回未经解码的原始输出
func runCmdRaw(name string, args ...string) (stdout, stderr []byte, code int, err error) {
	return runRaw(exec.Command(name, args...))
}

// runRaw 执行命令并返回原始输出
func runRaw(command *exec.Cmd) (stdout, stderr []byte, code int, err error) {
	var outBuf, errBuf bytes.Buffer
	command.Stdout = &outBuf
	command.Stderr = &errBuf

	runErr := command.Run()
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}

	return outBuf.Bytes(), errBuf.Bytes(), code, runErr
}

// runRawResult 执行命令并转换为 cmdResult
func runRawResult(command *exec.Cmd) (cmdResult, error) {
	stdout, stderr, code, err := runRaw(command)

	res := cmdResult{
		Stdout:     sanitizeText(string(stdout)),
		Stderr:     sanitizeText(string(stderr)),
		Returncode: code,
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			res.Err = err
		}
	}

	return res, err
}

type cmd struct {
	Build func() *exec.Cmd
}

func run(c cmd) cmdResult {
	command := c.Build()

	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	res := cmdResult{}
	err := command.Run()
	res.Stdout = sanitizeText(stdout.String())
	res.Stderr = sanitizeText(stderr.String())

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.Returncode = exitErr.ExitCode()
		} else {
			res.Returncode = -1
			res.Err = err
			res.Stderr = err.Error()
		}
	}

	return res
}

// sanitizeText 模拟 Python 以 utf-8/errors=replace 解码子进程输出
func sanitizeText(s string) string {
	return strings.ToValidUTF8(s, "\uFFFD")
}

// trimStdout 去除输出首尾空白
func trimStdout(res cmdResult) string {
	return strings.TrimSpace(res.Stdout)
}

// truncateText 按字符截断文本（对应 Python 的 s[:n]）
func truncateText(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// tailText 取文本末尾 n 个字符（对应 Python 的 s[-n:]）
func tailText(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[len(runes)-n:])
}
