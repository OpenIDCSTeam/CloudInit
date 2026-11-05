//go:build windows

package platform

import (
	"context"
	"os/exec"
	"syscall"
)

// newShellCmd 构造 Windows 下的 shell 执行命令（cmd.exe /c）
// 使用原始命令行，避免 Go 默认的参数转义破坏命令中的引号
func newShellCmd(command string) *exec.Cmd {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: "cmd.exe /c " + command}
	return cmd
}

// newShellCmdContext 构造带上下文的 Windows shell 执行命令
func newShellCmdContext(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: "cmd.exe /c " + command}
	return cmd
}
