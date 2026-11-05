//go:build !windows

package core

import (
	"context"
	"os/exec"
)

// newShellCommand 构造 shell 执行命令（等价于 subprocess.run(shell=True)）
func newShellCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "/bin/sh", "-c", command)
}
