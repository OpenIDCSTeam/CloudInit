//go:build windows

package core

import (
	"context"
	"os/exec"
	"syscall"
)

// newShellCommand 构造 shell 执行命令（等价于 subprocess.run(shell=True)）
// 使用原始命令行，保证带引号的命令与 Python 行为一致
func newShellCommand(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: "cmd.exe /c " + command}
	return cmd
}
