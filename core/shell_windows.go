//go:build windows

package core

import (
	"context"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// newShellCommand 构造 shell 执行命令（等价于 subprocess.run(shell=True)）
//
// 使用原始命令行保证带引号的命令与 Python 行为一致；
// 子进程使用独立隐藏控制台，防止 cls 等命令清空主程序日志。
func newShellCommand(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       "cmd.exe /c " + command,
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_CONSOLE,
	}
	return cmd
}
