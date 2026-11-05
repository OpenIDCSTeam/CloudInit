//go:build windows

package core

import (
	"os/exec"
	"syscall"
)

const (
	detachedProcess = 0x00000008
	createNoWindow  = 0x08000000
)

// startDetachedBatch 以完全独立的方式启动批处理脚本
func startDetachedBatch(batchPath string) error {
	cmd := exec.Command("cmd.exe", "/c", batchPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: detachedProcess | createNoWindow,
	}
	return cmd.Start()
}
