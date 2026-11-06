//go:build windows

package platform

import (
	"context"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// newShellCmd 构造 Windows 下的 shell 执行命令（cmd.exe /c）
//
// 使用原始命令行避免 Go 默认的参数转义破坏命令中的引号；
// 同时创建独立隐藏控制台，防止子进程（如激活脚本）里的 cls 清空主程序日志。
func newShellCmd(command string) *exec.Cmd {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = shellSysProcAttr("cmd.exe /c " + command)
	return cmd
}

// newShellCmdContext 构造带上下文的 Windows shell 执行命令
func newShellCmdContext(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd.exe")
	cmd.SysProcAttr = shellSysProcAttr("cmd.exe /c " + command)
	return cmd
}

// shellSysProcAttr 子进程使用独立隐藏控制台，避免影响主程序控制台输出
func shellSysProcAttr(cmdLine string) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CmdLine:       cmdLine,
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_CONSOLE,
	}
}
