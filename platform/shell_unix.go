//go:build !windows

package platform

import (
	"context"
	"os/exec"
)

// newShellCmd 构造 Unix 下的 shell 执行命令（/bin/sh -c）
func newShellCmd(command string) *exec.Cmd {
	return exec.Command("/bin/sh", "-c", command)
}

// newShellCmdContext 构造带上下文的 Unix shell 执行命令
func newShellCmdContext(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "/bin/sh", "-c", command)
}
